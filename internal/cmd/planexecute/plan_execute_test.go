package planexecute

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"idiom/internal/agent"
	"idiom/internal/app"
	"idiom/internal/cmd/runtime"
	"idiom/internal/comprehension"
	"idiom/internal/config"
	"idiom/internal/frontier/pathparse"
	"idiom/internal/planexecute"
	"idiom/internal/workspace"
)

func TestRunPlanExecuteMainForwardsThinkingAndMaxTokens(t *testing.T) {
	configPath := writePlanExecuteConfig(t)
	var stdout, stderr bytes.Buffer
	var gotOpts cmdruntime.RunOptions

	code := RunMain(
		[]string{"-config", configPath, "-prompt", "plan this", "-no-thinking", "-max-tokens", "2048"},
		&stdout,
		&stderr,
		func(_ config.Config, _ string, opts cmdruntime.RunOptions, _ io.Writer) (string, error) {
			gotOpts = opts
			return "no plan", nil
		},
	)

	if code != 1 {
		t.Fatalf("expected planner-without-artifact exit 1, got %d stderr=%s", code, stderr.String())
	}
	if !gotOpts.DisableThinking {
		t.Fatal("--no-thinking should reach the plan-execute planner run")
	}
	if gotOpts.MaxTokensOverride != 2048 {
		t.Fatalf("MaxTokensOverride = %d, want 2048", gotOpts.MaxTokensOverride)
	}
}

func TestRunPlanExecuteMainDelegatesPlannerOptionsAndModelOverride(t *testing.T) {
	configPath := writePlanExecuteConfig(t)
	var stdout, stderr bytes.Buffer
	var gotCfg config.Config
	var gotOpts cmdruntime.RunOptions
	var gotPrompt string

	code := RunMain(
		[]string{
			"-config", configPath,
			"-prompt", "plan adapter delegation",
			"-required-edit-path", "internal/cmd/planexecute/plan_execute.go",
			"-json",
			"-model", "planner-override",
			"-no-thinking",
			"-max-tokens", "3072",
		},
		&stdout,
		&stderr,
		func(cfg config.Config, prompt string, opts cmdruntime.RunOptions, _ io.Writer) (string, error) {
			gotCfg = cfg
			gotPrompt = prompt
			gotOpts = opts
			return "no plan", nil
		},
	)

	if code != 1 {
		t.Fatalf("expected planner-without-artifact exit 1, got %d stderr=%s", code, stderr.String())
	}
	if gotCfg.LlamaServer.Model != "planner-override" {
		t.Fatalf("model override = %q, want planner-override", gotCfg.LlamaServer.Model)
	}
	if strings.TrimSpace(gotCfg.Workspace.SessionID) == "" {
		t.Fatal("delegated run should stamp workspace session id before planner call")
	}
	if !strings.Contains(gotPrompt, "plan adapter delegation") {
		t.Fatalf("planner prompt should include original prompt, got:\n%s", gotPrompt)
	}
	if !gotOpts.JSONEvents || !gotOpts.DisableThinking {
		t.Fatalf("planner options should forward json/no-thinking flags, got %#v", gotOpts)
	}
	if gotOpts.TaskFamily != agent.PlanThenExecutePlannerTaskFamily || !gotOpts.PlannerOwnedVerification {
		t.Fatalf("planner options should carry plan-execute planner contract, got %#v", gotOpts)
	}
	if gotOpts.MaxTokensOverride != 3072 {
		t.Fatalf("MaxTokensOverride = %d, want 3072", gotOpts.MaxTokensOverride)
	}
	if gotOpts.ScopeContract.ContractID == "" || len(gotOpts.ScopeContract.RequiredCoveragePaths) != 1 {
		t.Fatalf("planner options should carry resolved scope contract, got %#v", gotOpts.ScopeContract)
	}
	events := planExecuteJSONEvents(t, stdout.String())
	if _, ok := planExecuteEventByType(events, "idiom.plan_execute.plan_artifact_missing"); !ok {
		t.Fatalf("delegated planner failure should preserve JSON event, got %s", stdout.String())
	}
}

func TestRunPlanExecuteMainUsesSharedExistingFilePlannerTokenBudget(t *testing.T) {
	configPath := writePlanExecuteConfig(t)
	var stdout, stderr bytes.Buffer
	var gotOpts cmdruntime.RunOptions

	code := RunMain(
		[]string{"-config", configPath, "-prompt", "plan this", "-required-edit-path", "internal/app/rendering.go"},
		&stdout,
		&stderr,
		func(_ config.Config, _ string, opts cmdruntime.RunOptions, _ io.Writer) (string, error) {
			gotOpts = opts
			return "no plan", nil
		},
	)

	if code != 1 {
		t.Fatalf("expected planner-without-artifact exit 1, got %d stderr=%s", code, stderr.String())
	}
	if gotOpts.MaxTokensOverride != 16384 {
		t.Fatalf("MaxTokensOverride = %d, want existing-file plan-execute budget 16384", gotOpts.MaxTokensOverride)
	}
}

func TestRunPlanExecuteMainKeepsLargeBudgetForNewFilePlanner(t *testing.T) {
	configPath := writePlanExecuteConfig(t)
	var stdout, stderr bytes.Buffer
	var gotOpts cmdruntime.RunOptions

	code := RunMain(
		[]string{"-config", configPath, "-prompt", "create new file with full file body", "-required-edit-path", "internal/app/new_feature.go"},
		&stdout,
		&stderr,
		func(_ config.Config, _ string, opts cmdruntime.RunOptions, _ io.Writer) (string, error) {
			gotOpts = opts
			return "no plan", nil
		},
	)

	if code != 1 {
		t.Fatalf("expected planner-without-artifact exit 1, got %d stderr=%s", code, stderr.String())
	}
	if gotOpts.MaxTokensOverride != 16384 {
		t.Fatalf("MaxTokensOverride = %d, want large-body plan-execute budget 16384", gotOpts.MaxTokensOverride)
	}
}

func TestRunPlanExecuteMainKeepsLargeBudgetForCreatedNamedPath(t *testing.T) {
	configPath := writePlanExecuteConfig(t)
	var stdout, stderr bytes.Buffer
	var gotOpts cmdruntime.RunOptions

	code := RunMain(
		[]string{"-config", configPath, "-prompt", "Create internal/writerroute/decision.go", "-required-edit-path", "internal/writerroute/decision.go"},
		&stdout,
		&stderr,
		func(_ config.Config, _ string, opts cmdruntime.RunOptions, _ io.Writer) (string, error) {
			gotOpts = opts
			return "no plan", nil
		},
	)

	if code != 1 {
		t.Fatalf("expected planner-without-artifact exit 1, got %d stderr=%s", code, stderr.String())
	}
	if gotOpts.MaxTokensOverride != 16384 {
		t.Fatalf("MaxTokensOverride = %d, want named create-path budget 16384", gotOpts.MaxTokensOverride)
	}
}

func TestRunPlanExecuteMainKeepsLargeBudgetForMissingRequestedTestPath(t *testing.T) {
	configPath := writePlanExecuteConfig(t)
	var stdout, stderr bytes.Buffer
	var gotOpts cmdruntime.RunOptions

	code := RunMain(
		[]string{"-config", configPath, "-prompt", "add regression tests proving the parser rejects invalid routes", "-required-edit-path", "internal/parser/route_test.go"},
		&stdout,
		&stderr,
		func(_ config.Config, _ string, opts cmdruntime.RunOptions, _ io.Writer) (string, error) {
			gotOpts = opts
			return "no plan", nil
		},
	)

	if code != 1 {
		t.Fatalf("expected planner-without-artifact exit 1, got %d stderr=%s", code, stderr.String())
	}
	if gotOpts.MaxTokensOverride != 16384 {
		t.Fatalf("MaxTokensOverride = %d, want missing requested-test budget 16384", gotOpts.MaxTokensOverride)
	}
}

func TestPlanExecutePromptProseDoesNotCreateEditSetObligations(t *testing.T) {
	for _, prompt := range []string{
		"Implement the feature and add tests.",
		"add a regression test for parser routes",
		"Harden temp symlink writes and add regression tests.",
	} {
		if obligations := planexecute.EditSetObligationsFromPrompt(prompt); len(obligations) != 0 {
			t.Fatalf("prompt prose must not create hidden edit-set obligations: %q -> %#v", prompt, obligations)
		}
	}
	for _, path := range []string{"tests/parser_test.py", "test/foo.py", "tests/panel_contract.gd"} {
		if !planExecutePathLooksLikeTest(path) {
			t.Fatalf("path should count as a test path: %s", path)
		}
	}
	if reason := (planexecute.EditSetObligations{}).With(planexecute.EditSetObligationKindImplementationEdit, "test").RejectionReason([]string{"docs/feature.md", "pkg/feature_test.go"}); reason != "missing_requested_implementation_edit" {
		t.Fatalf("docs plus tests should not satisfy implementation edit obligation, got %q", reason)
	}
}

func TestRunPlanExecuteMainJSONEmitsWriterRouteDecision(t *testing.T) {
	configPath := writePlanExecuteConfig(t)
	var stdout, stderr bytes.Buffer

	code := RunMain(
		[]string{"-config", configPath, "-prompt", "plan this change", "-json"},
		&stdout,
		&stderr,
		func(_ config.Config, _ string, _ cmdruntime.RunOptions, _ io.Writer) (string, error) {
			return "no plan", nil
		},
	)

	if code != 1 {
		t.Fatalf("expected planner-without-artifact exit 1, got %d stderr=%s", code, stderr.String())
	}
	events := planExecuteJSONEvents(t, stdout.String())
	event, ok := planExecuteEventByType(events, "idiom.plan_execute.writer_route_decision")
	if !ok {
		t.Fatalf("missing direct plan-execute writer route event in %s", stdout.String())
	}
	if event["writer_mode"] != "plan_submission" || event["lane"] != "plan-execute" || event["verification_owner"] != "planner_owned" {
		t.Fatalf("unexpected direct plan-execute route event: %#v", event)
	}
	if event["task_family"] != agent.PlanThenExecutePlannerTaskFamily {
		t.Fatalf("direct plan-execute route event should carry task_family, got %#v", event)
	}
	if event["cost_tier"] != "local_first" || event["review_policy"] != "local_first" {
		t.Fatalf("route event should carry local-first defaults: %#v", event)
	}
	if !jsonEventStringSliceContains(event["reason_codes"], "route.multi_file_plan_execute") {
		t.Fatalf("direct plan-execute route should explain multi-file plan-execute dispatch: %#v", event)
	}
}

func TestPlanExecutePlannerAddendumAvoidsHardReadCaps(t *testing.T) {
	addendum := planExecutePlannerSystemAddendum(nil, nil)
	for _, forbidden := range []string{
		"At most one bounded source-read",
		"Never re-read the same file",
	} {
		if strings.Contains(addendum, forbidden) {
			t.Fatalf("planner addendum still contains hard read-cap wording %q:\n%s", forbidden, addendum)
		}
	}
	for _, want := range []string{
		"new named edit-safety fact",
		"Do not re-read for confidence",
		"planner target packet",
		"missing body evidence",
		"`Decision: source-satisfied`",
		"candidate/target refs",
		"No required edit paths were provided",
		"real edits grounded in the objective/target refs",
		"Each edit body must be executable final content",
		"Do not submit placeholders, ellipses",
		"Prefer the smallest executable edit body",
		"real required or selected path",
		"use exact `old_text`/`new_text` for small unique replacements",
		"`insert_after_line` inserts small inline non-empty `content` after an existing 1-based line",
		"needs a large insertion anchored to unchanged source lines",
		"complete replacement for the inclusive range",
	} {
		if !strings.Contains(addendum, want) {
			t.Fatalf("planner addendum missing readiness-oriented wording %q:\n%s", want, addendum)
		}
	}
	if strings.Contains(addendum, "path/from/required_edit_paths") {
		t.Fatalf("planner addendum must not contain copyable placeholder paths:\n%s", addendum)
	}
}

func TestPlanExecutePlannerAddendumIncludesRequiredPathChecklist(t *testing.T) {
	addendum := planExecutePlannerSystemAddendum([]string{"a.cpp", "b.cpp", "a.cpp"}, nil)

	for _, want := range []string{
		"Required path coverage for this run: 2 required path(s).",
		"- a.cpp",
		"- b.cpp",
		"If at least one required path needs a change",
		"top-level `source_satisfied_paths` only for already-final paths",
		"If every required path already contains the requested final state, do not call `submit_plan`; answer `Decision: source-satisfied`",
		"Do not submit a partial plan",
	} {
		if !strings.Contains(addendum, want) {
			t.Fatalf("planner addendum missing %q:\n%s", want, addendum)
		}
	}
}

func TestPlanExecutePlannerAddendumIncludesAllowedPathChecklist(t *testing.T) {
	addendum := planExecutePlannerSystemAddendum(nil, []string{"internal/app/rendering.go", "internal/app/rendering.go"})

	for _, want := range []string{
		"Allowed `submit_plan` edit scope for this run: 1 candidate path(s).",
		"- internal/app/rendering.go",
		"allowlist, not coverage obligations",
	} {
		if !strings.Contains(addendum, want) {
			t.Fatalf("planner addendum missing %q:\n%s", want, addendum)
		}
	}
}

func TestPlanExecuteScopeToBriefBodyInsertsAllowedPathsInsideMarkers(t *testing.T) {
	prompt := pathparse.WrapBriefBody("# Objective\n\nUpdate the banner.")
	got := planExecuteScopeToBriefBody(prompt, nil, []string{"internal/app/rendering.go"})
	scoped := pathparse.ScopeToBriefBody(got)

	if !strings.Contains(scoped, "allowed_edit_paths:") || !strings.Contains(scoped, "internal/app/rendering.go") {
		t.Fatalf("allowed scope was not inserted inside brief body:\n%s", got)
	}
	if allowed := app.AllowedEditPathsFromPrompt(got); len(allowed) != 1 || allowed[0] != "internal/app/rendering.go" {
		t.Fatalf("allowed paths were not parseable from scoped prompt: %#v\n%s", allowed, got)
	}
}

func TestPlanExecuteEditSetObligationsDoNotPromoteAllowedPathsToRequiredPaths(t *testing.T) {
	prompt := `Implement the feature and add tests.

allowed_edit_paths:
- pkg/feature.go
- pkg/feature_test.go
`
	required := planExecuteRequiredEditPaths(planExecuteOptions{}, prompt)
	if len(required) != 0 {
		t.Fatalf("edit-set obligations and allowed scope must not define required paths, got %#v", required)
	}
	allowed := planExecuteAllowedEditPaths(planExecuteOptions{}, prompt)
	for _, want := range []string{"pkg/feature.go", "pkg/feature_test.go"} {
		if !containsString(allowed, want) {
			t.Fatalf("missing allowed path %q, got %#v", want, allowed)
		}
	}
	obligations := planexecute.EditSetObligationsFromPrompt(prompt)
	if len(obligations) != 0 {
		t.Fatalf("prompt prose must not create edit-set obligations, got %#v", obligations)
	}
}

func TestPlanExecuteScopeContractDoesNotReinferPromptObligationsWhenTypedScopePresent(t *testing.T) {
	prompt := strings.Join([]string{
		"## Context Pack",
		"",
		"Generated context mentions feature fallback and add tests.",
		"",
		pathparse.WrapBriefBody("# Objective\n\nImplement the feature and add tests."),
	}, "\n")
	opts := planExecuteOptions{
		ScopeContract: planexecute.NewPlanScopeContract(nil, []string{"pkg/feature.go", "pkg/feature_test.go"}, nil),
	}

	contract := planExecuteScopeContract(opts, prompt)
	if len(contract.EditSetObligations) != 0 {
		t.Fatalf("typed scope owns edit-set obligations; prompt prose must not re-infer them: %#v", contract.EditSetObligations)
	}
}

func TestPlanExecuteScopeContractPreservesTypedObligationSources(t *testing.T) {
	opts := planExecuteOptions{
		ScopeContract: planexecute.PlanScopeContract{
			EditSetObligations: planexecute.EditSetObligations{{Kind: planexecute.EditSetObligationKindTestEdit}},
		},
		EditSetObligations: planexecute.EditSetObligations{{Kind: planexecute.EditSetObligationKindImplementationEdit}},
	}

	contract := planExecuteScopeContract(opts, "")
	got := map[planexecute.EditSetObligationKind]string{}
	for _, obligation := range contract.EditSetObligations {
		got[obligation.Kind] = obligation.Source
	}
	if got[planexecute.EditSetObligationKindTestEdit] != "scope_contract" {
		t.Fatalf("scope contract obligation source = %q, want scope_contract; obligations=%#v", got[planexecute.EditSetObligationKindTestEdit], contract.EditSetObligations)
	}
	if got[planexecute.EditSetObligationKindImplementationEdit] != "options" {
		t.Fatalf("options obligation source = %q, want options; obligations=%#v", got[planexecute.EditSetObligationKindImplementationEdit], contract.EditSetObligations)
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
	if !agent.PlanThenExecuteSourceSatisfiedAnswerCoversPaths(answer, paths) {
		t.Fatalf("source-satisfied answer should cover named allowed evidence path")
	}
}

func TestPlanExecuteExactPlanEditsDuplicatesSharedPathBlock(t *testing.T) {
	prompt := "## Files To Modify\n\n" +
		"| File | Change |\n" +
		"|---|---|\n" +
		"| `a.h` | Update header. |\n" +
		"| `a.cpp` | Update implementation. |\n\n" +
		"## Required Edits\n\n" +
		"Update the `a.h` and `a.cpp` comments.\n\n" +
		"old_text:\n\n" +
		"```text\n// old shared comment\n```\n\n" +
		"new_text:\n\n" +
		"```text\n// new shared comment\n```\n"

	edits, reason := planExecuteExactPlanEdits(prompt, []string{"a.h", "a.cpp"})
	if reason != "" {
		t.Fatalf("unexpected parse skip reason: %s", reason)
	}
	if len(edits) != 2 {
		t.Fatalf("expected two duplicated edits, got %#v", edits)
	}
	got := map[string]workspace.PlanEdit{}
	for _, edit := range edits {
		got[edit.Path] = edit
	}
	for _, path := range []string{"a.h", "a.cpp"} {
		edit, ok := got[path]
		if !ok || edit.OldText != "// old shared comment" || edit.NewText != "// new shared comment" {
			t.Fatalf("shared edit missing for %s: %#v", path, edits)
		}
	}
}

func TestPlanExecuteExactPlanEditsDefaultsUnlabeledBlocksToSoleRequiredPath(t *testing.T) {
	prompt := "## Files To Modify\n\n" +
		"| File | Change |\n" +
		"|---|---|\n" +
		"| `suite.cpp` | Add focused tests. |\n\n" +
		"## Required Edits\n\n" +
		"### Required Edit 1 -- include constants\n\n" +
		"old_text:\n\n" +
		"```text\n#include \"old.h\"\n```\n\n" +
		"new_text:\n\n" +
		"```text\n#include \"old.h\"\n#include \"constants.h\"\n```\n\n" +
		"### Required Edit 2 -- append regression tests\n\n" +
		"old_text:\n\n" +
		"```text\n// next section\n```\n\n" +
		"new_text:\n\n" +
		"```text\nTEST_CASE(\"regression\") {}\n\n// next section\n```\n"

	edits, reason := planExecuteExactPlanEdits(prompt, []string{"suite.cpp"})
	if reason != "" {
		t.Fatalf("unexpected parse skip reason: %s", reason)
	}
	if len(edits) != 2 {
		t.Fatalf("expected both unlabeled exact edits to target the sole required path, got %#v", edits)
	}
	for _, edit := range edits {
		if edit.Path != "suite.cpp" {
			t.Fatalf("unlabeled edit should target suite.cpp, got %#v", edits)
		}
	}
	if !strings.Contains(edits[1].NewText, `TEST_CASE("regression")`) {
		t.Fatalf("second edit did not capture test body: %#v", edits[1])
	}
}

func TestPlanExecuteExactPlanEditsParsesBlockquotedRequiredBehaviorNewFileBody(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "writerroute"), 0o755); err != nil {
		t.Fatalf("mkdir writerroute: %v", err)
	}
	prompt := strings.Join([]string{
		"## Referenced Plan Documents",
		"",
		"> ## Files To Modify",
		">",
		"> | File | Change |",
		"> |---|---|",
		"> | `internal/writerroute/decision.go` | NEW FILE: add route decision type |",
		">",
		"> ## Required Behavior",
		">",
		"> 1. Create `internal/writerroute/decision.go` with the following content:",
		">",
		"> ```text",
		"> package writerroute",
		">",
		"> const SchemaVersion = \"idiom.writer_route.v1\"",
		"> ```",
	}, "\n")

	edits, reason := planExecuteExactPlanEditsForWorkspace(prompt, []string{"internal/writerroute/decision.go"}, root)
	if reason != "" {
		t.Fatalf("unexpected parse skip reason: %s", reason)
	}
	if len(edits) != 1 {
		t.Fatalf("expected one implicit new-file edit, got %#v", edits)
	}
	if edits[0].Path != "internal/writerroute/decision.go" || !edits[0].ReplaceAll || edits[0].OldText != "" {
		t.Fatalf("unexpected implicit new-file edit: %#v", edits[0])
	}
}

func TestPlanExecuteExactPlanEditsBindsImplicitBodyToNearestPathMention(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "writerroute"), 0o755); err != nil {
		t.Fatalf("mkdir writerroute: %v", err)
	}
	prompt := strings.Join([]string{
		"## Referenced Plan Documents",
		"",
		"> ## Required Behavior",
		">",
		"> 1. Create `internal/writerroute/decision.go` with the following content:",
		">",
		"> ```text",
		"> package writerroute",
		">",
		"> const SchemaVersion = \"idiom.writer_route.v1\"",
		"> ```",
		">",
		"> 2. Create `internal/writerroute/decision_test.go` with tests:",
		">",
		"> ```text",
		"> package writerroute",
		">",
		"> import \"testing\"",
		"> ```",
	}, "\n")
	edits, reason := planExecuteExactPlanEditsForWorkspace(prompt, []string{"internal/writerroute/decision.go"}, root)
	if reason != "" {
		t.Fatalf("unexpected parse skip reason: %s", reason)
	}
	if len(edits) != 1 {
		t.Fatalf("expected one implicit new-file edit, got %#v", edits)
	}
	if strings.Contains(edits[0].NewText, "testing") {
		t.Fatalf("implicit body parser captured out-of-scope test block: %q", edits[0].NewText)
	}
}

func TestRunPlanExecuteMainAutoExactPlanSkipsPlannerModel(t *testing.T) {
	configPath := writePlanExecuteConfig(t)
	root := filepath.Dir(configPath)
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("old a\n"), 0o644); err != nil {
		t.Fatalf("write a: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "b.txt"), []byte("old b\n"), 0o644); err != nil {
		t.Fatalf("write b: %v", err)
	}
	prompt := "## Files To Modify\n\n" +
		"| File | Change |\n" +
		"|---|---|\n" +
		"| `a.txt` | Update a. |\n" +
		"| `b.txt` | Update b. |\n\n" +
		"## Required Edits\n\n" +
		"In `a.txt`, replace the text.\n\n" +
		"old_text:\n\n```text\nold a\n```\n\n" +
		"new_text:\n\n```text\nnew a\n```\n\n" +
		"In `b.txt`, replace the text.\n\n" +
		"old_text:\n\n```text\nold b\n```\n\n" +
		"new_text:\n\n```text\nnew b\n```\n"
	var stdout, stderr bytes.Buffer
	modelCalled := false

	code := RunMain(
		[]string{"-config", configPath, "-prompt", prompt, "-json", "-skip-review-repair"},
		&stdout,
		&stderr,
		func(_ config.Config, _ string, _ cmdruntime.RunOptions, _ io.Writer) (string, error) {
			modelCalled = true
			return "", fmt.Errorf("planner model should not be called for complete exact-anchor packets")
		},
	)

	if code != 0 {
		t.Fatalf("auto exact plan should execute cleanly, got %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if modelCalled {
		t.Fatal("planner model was called despite complete exact-anchor packet")
	}
	if got := readFileString(t, filepath.Join(root, "a.txt")); got != "new a\n" {
		t.Fatalf("a.txt = %q", got)
	}
	if got := readFileString(t, filepath.Join(root, "b.txt")); got != "new b\n" {
		t.Fatalf("b.txt = %q", got)
	}
	if !strings.Contains(stdout.String(), "auto_exact_plan_submitted") {
		t.Fatalf("auto exact plan event missing from stdout=%s", stdout.String())
	}
}

func TestRunPlanExecuteMainAutoExactPlanSourceSatisfiedSkipsPlannerModel(t *testing.T) {
	configPath := writePlanExecuteConfig(t)
	root := filepath.Dir(configPath)
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("new a\n"), 0o644); err != nil {
		t.Fatalf("write a: %v", err)
	}
	prompt := "## Files To Modify\n\n" +
		"| File | Change |\n" +
		"|---|---|\n" +
		"| `a.txt` | Update a. |\n\n" +
		"## Required Edits\n\n" +
		"In `a.txt`, replace the text.\n\n" +
		"old_text:\n\n```text\nold a\n```\n\n" +
		"new_text:\n\n```text\nnew a\n```\n"
	var stdout, stderr bytes.Buffer
	modelCalled := false

	code := RunMain(
		[]string{"-config", configPath, "-prompt", prompt, "-json", "-skip-review-repair"},
		&stdout,
		&stderr,
		func(_ config.Config, _ string, _ cmdruntime.RunOptions, _ io.Writer) (string, error) {
			modelCalled = true
			return "", fmt.Errorf("planner model should not be called for source-satisfied exact-anchor packets")
		},
	)

	if code != 0 {
		t.Fatalf("source-satisfied exact plan should pass, got %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if modelCalled {
		t.Fatal("planner model was called despite source-satisfied exact-anchor packet")
	}
	if got := readFileString(t, filepath.Join(root, "a.txt")); got != "new a\n" {
		t.Fatalf("a.txt = %q", got)
	}
	for _, want := range []string{"writer_route_decision", "auto_exact_plan_source_satisfied", "source_satisfied"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("expected stdout to contain %q, got %s", want, stdout.String())
		}
	}
}

func TestRunPlanExecuteMainAutoExactPlanInfersUnlabeledMultiFileAndNewFile(t *testing.T) {
	configPath := writePlanExecuteConfig(t)
	root := filepath.Dir(configPath)
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("alpha\nomega\n"), 0o644); err != nil {
		t.Fatalf("write a: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "b.txt"), []byte("ticks row\nfooter\n"), 0o644); err != nil {
		t.Fatalf("write b: %v", err)
	}
	prompt := "## Files To Modify\n\n" +
		"| File | Change |\n" +
		"|---|---|\n" +
		"| `a.txt` | Update first existing file. |\n" +
		"| `b.txt` | Update second existing file. |\n" +
		"| `c.json` | New file target. Add generated fixture. |\n\n" +
		"## Required Edits\n\n" +
		"1. Add the first update.\n\n" +
		"old_text:\n\n```text\nalpha\n```\n\n" +
		"new_text:\n\n```text\nalpha\nseed\n```\n\n" +
		"2. Add the second update.\n\n" +
		"old_text:\n\n```text\nticks row\n```\n\n" +
		"new_text:\n\n```text\nticks row\nlast result\n```\n\n" +
		"3. Create new file `c.json`.\n\n" +
		"replace_all: true\n\n" +
		"```text\n{\"ok\": true}\n```\n"
	var stdout, stderr bytes.Buffer
	modelCalled := false

	code := RunMain(
		[]string{"-config", configPath, "-prompt", prompt, "-json", "-skip-review-repair"},
		&stdout,
		&stderr,
		func(_ config.Config, _ string, _ cmdruntime.RunOptions, _ io.Writer) (string, error) {
			modelCalled = true
			return "", fmt.Errorf("planner model should not be called for inferable exact-anchor packets")
		},
	)

	if code != 0 {
		t.Fatalf("auto exact plan should infer multi-file paths and new-file body, got %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if modelCalled {
		t.Fatal("planner model was called despite inferable exact-anchor packet")
	}
	if got := readFileString(t, filepath.Join(root, "a.txt")); got != "alpha\nseed\nomega\n" {
		t.Fatalf("a.txt = %q", got)
	}
	if got := readFileString(t, filepath.Join(root, "b.txt")); got != "ticks row\nlast result\nfooter\n" {
		t.Fatalf("b.txt = %q", got)
	}
	if got := readFileString(t, filepath.Join(root, "c.json")); got != "{\"ok\": true}" {
		t.Fatalf("c.json = %q", got)
	}
	if !strings.Contains(stdout.String(), "auto_exact_plan_submitted") {
		t.Fatalf("auto exact plan event missing from stdout=%s", stdout.String())
	}
}

func TestRunPlanExecuteMainRequiredEditPathFlagEnablesAutoExactPlan(t *testing.T) {
	configPath := writePlanExecuteConfig(t)
	root := filepath.Dir(configPath)
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("old a\n"), 0o644); err != nil {
		t.Fatalf("write a: %v", err)
	}
	prompt := "## Required Edits\n\n" +
		"old_text:\n\n```text\nold a\n```\n\n" +
		"new_text:\n\n```text\nnew a\n```\n"
	var stdout, stderr bytes.Buffer
	modelCalled := false

	code := RunMain(
		[]string{
			"-config", configPath,
			"-prompt", prompt,
			"-json",
			"-skip-review-repair",
			"--required-edit-path", "./a.txt",
		},
		&stdout,
		&stderr,
		func(_ config.Config, _ string, _ cmdruntime.RunOptions, _ io.Writer) (string, error) {
			modelCalled = true
			return "", fmt.Errorf("planner model should not be called when required-edit-path enables exact-anchor plan")
		},
	)

	if code != 0 {
		t.Fatalf("auto exact plan should execute cleanly with explicit required path, got %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if modelCalled {
		t.Fatal("planner model was called despite explicit required path and complete exact-anchor packet")
	}
	if got := readFileString(t, filepath.Join(root, "a.txt")); got != "new a\n" {
		t.Fatalf("a.txt = %q", got)
	}
	out := stdout.String()
	if !strings.Contains(out, "required_edit_paths_resolved") || !strings.Contains(out, "auto_exact_plan_submitted") {
		t.Fatalf("expected required-path resolution and auto-plan events, got stdout=%s", out)
	}
	if strings.Contains(out, "no required_edit_paths") {
		t.Fatalf("explicit required path should avoid no-required-path skip, stdout=%s", out)
	}
}

func TestRunPlanExecuteMainTreatsSourceSatisfiedPlannerAnswerAsCleanNoop(t *testing.T) {
	configPath := writePlanExecuteConfig(t)
	prompt := "## Files To Modify\n\n| File | Change |\n|------|--------|\n| `a.go` | update existing behavior |\n| `b.go` | update existing test |\n"
	var stdout, stderr bytes.Buffer

	code := RunMain(
		[]string{"-config", configPath, "-prompt", prompt},
		&stdout,
		&stderr,
		func(_ config.Config, _ string, _ cmdruntime.RunOptions, _ io.Writer) (string, error) {
			return "Decision: source-satisfied\n- a.go: already implemented; no changes needed.\n- b.go: already implemented; no changes needed.", nil
		},
	)

	if code != 0 {
		t.Fatalf("source-satisfied planner answer should exit 0, got %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if strings.Contains(stderr.String(), "plan artifact") {
		t.Fatalf("source-satisfied closeout should not report missing plan artifact, stderr=%s", stderr.String())
	}
	if !strings.Contains(stdout.String(), "source-satisfied: executor skipped") {
		t.Fatalf("source-satisfied closeout should be visible, stdout=%s", stdout.String())
	}
}

func TestRunPlanExecuteMainTreatsUnscopedSourceSatisfiedPlannerAnswerAsCleanNoop(t *testing.T) {
	configPath := writePlanExecuteConfig(t)
	prompt := "Reject temp symlinks and add regression tests proving no partial mutation."
	var stdout, stderr bytes.Buffer

	code := RunMain(
		[]string{"-config", configPath, "-prompt", prompt},
		&stdout,
		&stderr,
		func(_ config.Config, _ string, _ cmdruntime.RunOptions, _ io.Writer) (string, error) {
			return "Decision: source-satisfied\n- internal/productworkflow/migration_apply.go: no changes needed.\n- internal/productworkflow/migration_apply_test.go: no changes needed.", nil
		},
	)

	if code != 0 {
		t.Fatalf("unscoped source-satisfied planner answer should exit 0, got %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if strings.Contains(stderr.String(), "plan artifact") {
		t.Fatalf("source-satisfied closeout should not report missing plan artifact, stderr=%s", stderr.String())
	}
	if !strings.Contains(stdout.String(), "source-satisfied: executor skipped") {
		t.Fatalf("source-satisfied closeout should be visible, stdout=%s", stdout.String())
	}
}

func TestPlanExecuteMissingSourceSatisfiedCoveragePathsUsesExactCoverage(t *testing.T) {
	answer := "Decision: source-satisfied\n- pkg/parser.go.bak: already present.\n- pkg/parser_test.go: already present."
	got := planExecuteMissingSourceSatisfiedCoveragePaths(answer, []string{"pkg/parser.go", "pkg/parser_test.go"})
	if len(got) != 1 || got[0] != "pkg/parser.go" {
		t.Fatalf("missing source-satisfied coverage paths = %#v, want pkg/parser.go", got)
	}
}

func TestPlanExecuteAutoSourceSatisfiedAnswerCoversRequiredPaths(t *testing.T) {
	answer := planExecuteAutoSourceSatisfiedAnswer([]string{"pkg/parser.go", "pkg/parser_test.go"})
	got := planExecuteMissingSourceSatisfiedCoveragePaths(answer, []string{"pkg/parser.go", "pkg/parser_test.go"})
	if len(got) != 0 {
		t.Fatalf("auto source-satisfied answer should cover required paths, missing %#v in:\n%s", got, answer)
	}
}

func TestRunPlanExecuteMainDoesNotInferImplementationObligationFromPrompt(t *testing.T) {
	configPath := writePlanExecuteConfig(t)
	prompt := "Implement the feature and add tests."
	var stdout, stderr bytes.Buffer

	code := RunMain(
		[]string{"-config", configPath, "-prompt", prompt, "-json"},
		&stdout,
		&stderr,
		func(_ config.Config, _ string, _ cmdruntime.RunOptions, _ io.Writer) (string, error) {
			return "Decision: source-satisfied\n- pkg/feature_test.go: feature tests are already present.", nil
		},
	)

	if code != 0 {
		t.Fatalf("prompt prose should not create a hidden implementation obligation, got %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	events := planExecuteJSONEvents(t, stdout.String())
	if _, ok := planExecuteEventByType(events, "idiom.plan_execute.source_satisfied_rejected"); ok {
		t.Fatalf("prompt prose must not produce source_satisfied_rejected, got %s", stdout.String())
	}
}

func TestRunPlanExecuteMainStillFailsMissingArtifactWithoutSourceSatisfiedEvidence(t *testing.T) {
	configPath := writePlanExecuteConfig(t)
	prompt := "## Files To Modify\n\n| File | Change |\n|------|--------|\n| `a.go` | update existing behavior |\n| `b.go` | update existing test |\n"
	var stdout, stderr bytes.Buffer

	code := RunMain(
		[]string{"-config", configPath, "-prompt", prompt},
		&stdout,
		&stderr,
		func(_ config.Config, _ string, _ cmdruntime.RunOptions, _ io.Writer) (string, error) {
			return "Decision: source-satisfied\n- a.go: already implemented; no changes needed.", nil
		},
	)

	if code != 1 {
		t.Fatalf("missing source-satisfied path evidence should still fail missing artifact, got %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stderr.String(), "planner phase did not produce a plan artifact") {
		t.Fatalf("expected normal missing-artifact failure, stderr=%s", stderr.String())
	}
}

func TestRunPlanExecuteMainFailsMissingArtifactDespiteExplicitFullBodyNewFileBrief(t *testing.T) {
	configPath := writePlanExecuteConfig(t)
	root := filepath.Dir(configPath)
	prompt := "## Files To Modify\n\n" +
		"| File | Change |\n" +
		"|------|--------|\n" +
		"| `docs/generated-note.txt` | Create the new note. |\n\n" +
		"## SubmitPlan Shortcut\n\n" +
		"This is a new-file atom. Use this full file body for the `replace_all:true` / empty `old_text` / `new_text` submit_plan shape:\n\n" +
		"```text\n" +
		"alpha\n" +
		"beta\n" +
		"```\n"
	var stdout, stderr bytes.Buffer

	code := RunMain(
		[]string{"-config", configPath, "-prompt", prompt, "-json", "-skip-review-repair"},
		&stdout,
		&stderr,
		func(_ config.Config, _ string, _ cmdruntime.RunOptions, _ io.Writer) (string, error) {
			return "I can create docs/generated-note.txt, but I did not call submit_plan.", nil
		},
	)

	if code != 1 {
		t.Fatalf("missing planner artifact must fail even with inline full body, got %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stderr.String(), "planner phase did not produce a plan artifact") {
		t.Fatalf("expected missing-artifact failure, stderr=%s", stderr.String())
	}
	if _, err := os.Stat(filepath.Join(root, "docs", "generated-note.txt")); !os.IsNotExist(err) {
		t.Fatalf("host fallback must not create a file from prompt body, stat err=%v", err)
	}
	if strings.Contains(stdout.String(), "idiom.plan_execute.auto_submit_full_body_plan") {
		t.Fatalf("host fallback telemetry must not be emitted, stdout=%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "idiom.plan_execute.plan_artifact_missing") {
		t.Fatalf("expected missing-artifact telemetry event, stdout=%s", stdout.String())
	}
}

func TestRunPlanExecuteMainDoesNotAutoSubmitRealQuestionBlocker(t *testing.T) {
	configPath := writePlanExecuteConfig(t)
	root := filepath.Dir(configPath)
	prompt := "## Files To Modify\n\n" +
		"| File | Change |\n" +
		"|------|--------|\n" +
		"| `docs/generated-note.txt` | Create the new note. |\n\n" +
		"## SubmitPlan Shortcut\n\n" +
		"Use this full file body for the `replace_all:true` / empty `old_text` / `new_text` submit_plan shape:\n\n" +
		"```text\n" +
		"alpha\n" +
		"```\n"
	var stdout, stderr bytes.Buffer

	code := RunMain(
		[]string{"-config", configPath, "-prompt", prompt, "-skip-review-repair"},
		&stdout,
		&stderr,
		func(_ config.Config, _ string, _ cmdruntime.RunOptions, _ io.Writer) (string, error) {
			return "<<QUESTION>>: docs/generated-note.txt: current API evidence contradicts the requested body; do not create the file until the brief is rebased.", nil
		},
	)

	if code != 1 {
		t.Fatalf("real question blockers must not be auto-submitted, got %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if _, err := os.Stat(filepath.Join(root, "docs", "generated-note.txt")); !os.IsNotExist(err) {
		t.Fatalf("auto-submit created a file despite a real blocker, stat err=%v", err)
	}
	if !strings.Contains(stderr.String(), "planner phase reported a blocker before submitting a plan") {
		t.Fatalf("explicit blocker should be surfaced as planner_blocked, stderr=%s", stderr.String())
	}
	if strings.Contains(stderr.String(), "planner phase did not produce a plan artifact") {
		t.Fatalf("explicit blocker should not be masked as missing artifact, stderr=%s", stderr.String())
	}
	records, err := filepath.Glob(filepath.Join(root, ".idiom", "recovery", "plan-execute-*", "failure.json"))
	if err != nil {
		t.Fatalf("glob recovery records: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected one automatic recovery record, got %d: %v", len(records), records)
	}
	if body := readFileString(t, records[0]); !strings.Contains(body, `"class": "planner_blocked"`) ||
		!strings.Contains(body, `"next_valid_action": "answer_blocker"`) {
		t.Fatalf("unexpected recovery record:\n%s", body)
	}
}

func TestRunPlanExecuteMainDoesNotTreatQuestionBlockerMentioningSourceSatisfiedAsCleanNoop(t *testing.T) {
	configPath := writePlanExecuteConfig(t)
	prompt := "## Files To Modify\n\n| File | Change |\n|------|--------|\n| `a.go` | update existing behavior |\n"
	var stdout, stderr bytes.Buffer

	code := RunMain(
		[]string{"-config", configPath, "-prompt", prompt},
		&stdout,
		&stderr,
		func(_ config.Config, _ string, _ cmdruntime.RunOptions, _ io.Writer) (string, error) {
			return "<<QUESTION>>: Completion gate rejected before submit_plan.\n- a.go: no successful edit was recorded; provide submit_plan, Decision: source-satisfied evidence, or a path-specific blocker.", nil
		},
	)

	if code != 1 {
		t.Fatalf("blocker that merely mentions source-satisfied must fail typed blocker, got %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stderr.String(), "planner phase reported a blocker before submitting a plan") {
		t.Fatalf("expected typed planner blocker for blocker answer, stderr=%s", stderr.String())
	}
	if strings.Contains(stderr.String(), "planner phase did not produce a plan artifact") {
		t.Fatalf("blocker should not be masked as missing artifact, stderr=%s", stderr.String())
	}
	if strings.Contains(stdout.String(), "source-satisfied: executor skipped") {
		t.Fatalf("blocker must not be reported as source-satisfied, stdout=%s", stdout.String())
	}
}

func TestPlanExecuteInheritsPromptContextPackID(t *testing.T) {
	prompt := "## Context Pack\n\n- Summary: context view artifact\n- Pack: `feedfacecafebeef`\n"
	plan := workspace.PlanArtifact{SchemaVersion: workspace.PlanArtifactSchemaVersion}
	got := inheritPlanExecutePromptPackID(&plan, prompt)
	if got != "feedfacecafebeef" || plan.PackID != "feedfacecafebeef" {
		t.Fatalf("expected inherited pack_id feedfacecafebeef, got %q plan=%#v", got, plan)
	}
	if got := extractPlanExecutePromptPackID("context_pack://0123456789abcdef"); got != "0123456789abcdef" {
		t.Fatalf("expected context_pack URI extraction, got %q", got)
	}
}

func TestTruncatePlanExecuteTextPreservesUTF8(t *testing.T) {
	got := truncatePlanExecuteText("alpha βeta", 8)
	if !strings.Contains(got, "[truncated") {
		t.Fatalf("expected truncation marker, got %q", got)
	}
	if !utf8.ValidString(got) {
		t.Fatalf("truncatePlanExecuteText must not split a UTF-8 rune, got %q", got)
	}
}

func TestRunPlanExecuteMainInheritsPackBeforeExecutor(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/planexec\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatalf("seed go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package pkg\n\nfunc A() string { return \"alpha\" }\n"), 0o644); err != nil {
		t.Fatalf("seed a.go: %v", err)
	}
	build, err := comprehension.Build(t.Context(), comprehension.BuildOptions{Root: root, Language: "go"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	key := comprehension.ViewKey{IndexID: build.IndexID, Task: "inherit pack", Mode: "plan"}
	packID := key.ID()
	if err := comprehension.SaveView(root, key, comprehension.PackResult{
		PackID:         packID,
		IndexID:        build.IndexID,
		Root:           root,
		Task:           "inherit pack",
		Mode:           "plan",
		Fresh:          true,
		CandidateFiles: []comprehension.PackFile{{Path: "a.go", Role: "editable"}},
	}); err != nil {
		t.Fatalf("SaveView: %v", err)
	}
	configPath := filepath.Join(root, "config.json")
	body := fmt.Sprintf(`{"workspace":{"root":%q,"cwd":%q,"enable_writes":true}}`, root, root)
	if err := os.WriteFile(configPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	prompt := "## Context Pack\n\n- Pack: `" + packID + "`\n\nChange a.go from alpha to beta."
	var stdout, stderr bytes.Buffer
	code := RunMain(
		[]string{"-config", configPath, "-json", "-skip-review-repair", "-prompt", prompt},
		&stdout,
		&stderr,
		func(cfg config.Config, _ string, _ cmdruntime.RunOptions, _ io.Writer) (string, error) {
			writePlanExecuteTestArtifact(t, cfg.Workspace.Root, cfg.Workspace.SessionID, workspace.PlanArtifact{
				SchemaVersion: workspace.PlanArtifactSchemaVersion,
				Created:       time.Now().UTC().Format(time.RFC3339Nano),
				SessionID:     cfg.Workspace.SessionID,
				Rationale:     "change marker",
				Edits: []workspace.PlanEdit{
					{Path: "a.go", OldText: "alpha", NewText: "beta", Purpose: "change marker"},
				},
			})
			return "submitted", nil
		},
	)
	if code != 0 {
		t.Fatalf("RunMain exit=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), `idiom.plan_execute.plan_artifact_pack_inherited`) {
		t.Fatalf("expected inherited pack event, got:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), `idiom.plan_execute.executor_inherits_pack`) {
		t.Fatalf("expected executor to inherit pack after prompt inheritance, got:\n%s", stdout.String())
	}
}

func TestRunPlanExecuteMainDoesNotInferTestEditObligationBeforeWrites(t *testing.T) {
	configPath := writePlanExecuteConfig(t)
	root := filepath.Dir(configPath)
	if err := os.WriteFile(filepath.Join(root, "pkg.go"), []byte("package demo\n\nfunc Hardened() bool { return false }\n"), 0o644); err != nil {
		t.Fatalf("seed pkg.go: %v", err)
	}
	var stdout, stderr bytes.Buffer

	code := RunMain(
		[]string{
			"-config", configPath,
			"-prompt", "Implement the hardening and add regression tests proving the edge case is rejected.",
			"-json",
			"-skip-review-repair",
		},
		&stdout,
		&stderr,
		func(cfg config.Config, _ string, opts cmdruntime.RunOptions, _ io.Writer) (string, error) {
			if opts.TaskFamily != agent.PlanThenExecutePlannerTaskFamily {
				t.Fatalf("unexpected task family %q", opts.TaskFamily)
			}
			writePlanExecuteTestArtifact(t, cfg.Workspace.Root, cfg.Workspace.SessionID, workspace.PlanArtifact{
				SchemaVersion: workspace.PlanArtifactSchemaVersion,
				Created:       time.Now().UTC().Format(time.RFC3339Nano),
				SessionID:     cfg.Workspace.SessionID,
				Rationale:     "production-only plan",
				Edits: []workspace.PlanEdit{
					{Path: "pkg.go", OldText: "return false", NewText: "return true", Purpose: "harden behavior"},
				},
			})
			return "submitted production-only plan", nil
		},
	)

	if code != 0 {
		t.Fatalf("prompt prose should not create a hidden test obligation, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if got := readFileString(t, filepath.Join(root, "pkg.go")); !strings.Contains(got, "return true") {
		t.Fatalf("production edit should apply when no typed test obligation exists, got:\n%s", got)
	}
}

func TestRunPlanExecuteMainDoesNotInferImplementationObligationBeforeWrites(t *testing.T) {
	configPath := writePlanExecuteConfig(t)
	root := filepath.Dir(configPath)
	if err := os.WriteFile(filepath.Join(root, "feature.go"), []byte("package demo\n\nfunc Enabled() bool { return false }\n"), 0o644); err != nil {
		t.Fatalf("seed feature.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "feature_test.go"), []byte("package demo\n\nfunc TestEnabled(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatalf("seed feature_test.go: %v", err)
	}
	var stdout, stderr bytes.Buffer

	code := RunMain(
		[]string{
			"-config", configPath,
			"-prompt", "Implement the feature. Start by adding failing tests, then implement.",
			"-json",
			"-skip-review-repair",
		},
		&stdout,
		&stderr,
		func(cfg config.Config, _ string, opts cmdruntime.RunOptions, _ io.Writer) (string, error) {
			if opts.TaskFamily != agent.PlanThenExecutePlannerTaskFamily {
				t.Fatalf("unexpected task family %q", opts.TaskFamily)
			}
			writePlanExecuteTestArtifact(t, cfg.Workspace.Root, cfg.Workspace.SessionID, workspace.PlanArtifact{
				SchemaVersion: workspace.PlanArtifactSchemaVersion,
				Created:       time.Now().UTC().Format(time.RFC3339Nano),
				SessionID:     cfg.Workspace.SessionID,
				Rationale:     "test-only plan",
				Edits: []workspace.PlanEdit{
					{Path: "feature_test.go", OldText: "func TestEnabled(t *testing.T) {}\n", NewText: "func TestEnabled(t *testing.T) {\n\tif !Enabled() {\n\t\tt.Fatal(\"disabled\")\n\t}\n}\n", Purpose: "add feature test"},
				},
			})
			return "submitted test-only plan", nil
		},
	)

	if code != 0 {
		t.Fatalf("prompt prose should not create a hidden implementation obligation, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if got := readFileString(t, filepath.Join(root, "feature_test.go")); !strings.Contains(got, "t.Fatal") {
		t.Fatalf("test-only edit should apply when no typed implementation obligation exists, got:\n%s", got)
	}
}

func writePlanExecuteConfig(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("IDIOM_QUEUE_SOCKET", filepath.Join(root, "missing-runner.sock"))
	path := filepath.Join(root, "config.json")
	body := fmt.Sprintf(`{"workspace":{"root":%q,"cwd":%q}}`, root, root)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func readFileString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func initGitRepoForStaleness(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "test"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	return root
}

func gitCommit(t *testing.T, root, path, content, message string) {
	t.Helper()
	full := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	for _, args := range [][]string{
		{"add", path},
		{"commit", "-q", "-m", message},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
}

func mustTime(value string) time.Time {
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		panic(err)
	}
	return t
}

func slicesContainString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func planExecuteJSONEvents(t *testing.T, stdout string) []map[string]any {
	t.Helper()
	var events []map[string]any
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("decode JSON event %q: %v", line, err)
		}
		events = append(events, event)
	}
	return events
}

func planExecuteEventByType(events []map[string]any, eventType string) (map[string]any, bool) {
	for _, event := range events {
		if event["type"] == eventType {
			return event, true
		}
	}
	return nil, false
}

func jsonEventStringSliceContains(value any, want string) bool {
	switch items := value.(type) {
	case []any:
		for _, item := range items {
			if item == want {
				return true
			}
		}
	case []string:
		for _, item := range items {
			if item == want {
				return true
			}
		}
	}
	return false
}

func TestRunExecutorPhaseEmitsExecutorInheritsPackEvent(t *testing.T) {
	// When the planner stamps a pack_id and the cache row exists, the
	// executor phase emits an executor_inherits_pack JSON event so
	// downstream tooling can attach the planner's ranked context.
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/planexec\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatalf("seed go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package pkg\n\nfunc A() string { return \"alpha\" }\n"), 0o644); err != nil {
		t.Fatalf("seed a.go: %v", err)
	}
	build, err := comprehension.Build(t.Context(), comprehension.BuildOptions{Root: root, Language: "go"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	key := comprehension.ViewKey{IndexID: build.IndexID, Task: "exec inherits", Mode: "plan"}
	cached := comprehension.PackResult{
		PackID:         key.ID(),
		IndexID:        build.IndexID,
		Root:           root,
		Task:           "exec inherits",
		Mode:           "plan",
		Fresh:          true,
		CandidateFiles: []comprehension.PackFile{{Path: "a.go", Role: "editable"}},
		CandidateSymbols: []comprehension.PackSymbol{
			{ID: "symbol://a.go#A", Kind: "function", Path: "a.go"},
		},
		Tests: []comprehension.PackTest{{Command: "go test ./...", Path: "a.go"}},
	}
	if err := comprehension.SaveView(root, key, cached); err != nil {
		t.Fatalf("SaveView: %v", err)
	}

	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		PackID:        key.ID(),
		Edits: []workspace.PlanEdit{
			{Path: "a.go", OldText: "alpha", NewText: "ALPHA"},
		},
	}
	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}
	var stdout bytes.Buffer
	if _, err := runExecutorPhase(cfg, plan, nil, true, &stdout); err != nil {
		t.Fatalf("executor phase failed: %v", err)
	}
	if !strings.Contains(stdout.String(), `idiom.plan_execute.executor_inherits_pack`) {
		t.Fatalf("expected executor_inherits_pack JSON event in stdout, got: %s", stdout.String())
	}
	events := planExecuteJSONEvents(t, stdout.String())
	handoff, ok := planExecuteEventByType(events, "idiom.plan_execute.executor_handoff_packet")
	if !ok {
		t.Fatalf("expected executor_handoff_packet event, got: %s", stdout.String())
	}
	if handoff["packet_state"] != executorHandoffStateInherited || handoff["reason"] != "fresh_graph_view" {
		t.Fatalf("expected inherited handoff packet, got %#v", handoff)
	}
	if !strings.Contains(stdout.String(), `"pack_id":"`+key.ID()+`"`) {
		t.Fatalf("expected pack_id %s in event, got: %s", key.ID(), stdout.String())
	}
	if !strings.Contains(stdout.String(), `"candidate_files":1`) {
		t.Fatalf("expected candidate_files:1 in event, got: %s", stdout.String())
	}
}

func TestRunExecutorPhaseEmitsExecutorPackMissOnUnknownPackID(t *testing.T) {
	// pack_id that doesn't match any graph_views row → executor_pack_miss
	// event so downstream consumers know the planner's context wasn't
	// available (rather than silently degrading).
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("alpha\n"), 0o644); err != nil {
		t.Fatalf("seed a.txt: %v", err)
	}
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		PackID:        "deadbeefcafef00d",
		Edits: []workspace.PlanEdit{
			{Path: "a.txt", OldText: "alpha", NewText: "ALPHA"},
		},
	}
	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}
	var stdout bytes.Buffer
	if _, err := runExecutorPhase(cfg, plan, nil, true, &stdout); err != nil {
		t.Fatalf("executor phase failed: %v", err)
	}
	if !strings.Contains(stdout.String(), `idiom.plan_execute.executor_pack_miss`) {
		t.Fatalf("expected executor_pack_miss JSON event when pack_id is unknown, got: %s", stdout.String())
	}
	events := planExecuteJSONEvents(t, stdout.String())
	handoff, ok := planExecuteEventByType(events, "idiom.plan_execute.executor_handoff_packet")
	if !ok || handoff["packet_state"] != executorHandoffStateMissing || handoff["reason"] != "cache_miss" {
		t.Fatalf("expected missing handoff packet, got %#v in %s", handoff, stdout.String())
	}
}

func TestRunExecutorPhaseEmitsMissingPacketHandoffWithoutPlanPacket(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("alpha\n"), 0o644); err != nil {
		t.Fatalf("seed a.txt: %v", err)
	}
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		PlanID:        "plan-missing-packet",
		Handoff:       workspace.PlanArtifactHandoff{ID: "handoff-1", Status: "ready", Ready: true},
		Edits:         []workspace.PlanEdit{{Path: "a.txt", OldText: "alpha", NewText: "ALPHA"}},
	}
	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}
	var stdout bytes.Buffer
	if _, err := runExecutorPhase(cfg, plan, nil, true, &stdout); err != nil {
		t.Fatalf("executor phase failed: %v", err)
	}
	events := planExecuteJSONEvents(t, stdout.String())
	handoff, ok := planExecuteEventByType(events, "idiom.plan_execute.executor_handoff_packet")
	if !ok {
		t.Fatalf("expected executor_handoff_packet event, got %s", stdout.String())
	}
	if handoff["packet_state"] != executorHandoffStateMissing || handoff["reason"] != "missing_packet_id" {
		t.Fatalf("expected missing packet state, got %#v", handoff)
	}
}

func TestRunExecutorPhaseBlocksPartialApprovalScopeDrift(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("alpha\n"), 0o644); err != nil {
		t.Fatalf("seed a.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "b.txt"), []byte("beta\n"), 0o644); err != nil {
		t.Fatalf("seed b.txt: %v", err)
	}
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		PlanID:        "plan raw_prompt customer-acme token=/Users/ben/private/client",
		Task: workspace.PlanArtifactTask{
			Summary:               "Block pending partial approval scope",
			ClientClassifications: []string{"finance"},
		},
		Paths: workspace.PlanArtifactPaths{Editable: []string{"a.txt", "b.txt"}, Changed: []string{"b.txt"}},
		Approval: workspace.PlanArtifactApproval{
			Required:            true,
			Status:              "partial",
			ScopeID:             "scope raw_prompt customer-acme token=/Users/ben/private/client",
			ApprovedPaths:       []string{"a.txt"},
			PendingPaths:        []string{"b.txt"},
			ExecutionPermission: "execute_approved_scope",
		},
		Handoff: workspace.PlanArtifactHandoff{ID: "handoff raw_prompt customer-acme", Status: "ready", Ready: true},
		Edits:   []workspace.PlanEdit{{Path: "b.txt", OldText: "beta", NewText: "BETA"}},
	}
	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}
	var stdout bytes.Buffer
	if _, err := runExecutorPhase(cfg, plan, nil, true, &stdout); err == nil {
		t.Fatalf("executor should block pending partial approval scope")
	}
	events := planExecuteJSONEvents(t, stdout.String())
	drift, ok := planExecuteEventByType(events, "idiom.plan_execute.plan_scope_drift")
	if !ok {
		t.Fatalf("expected plan_scope_drift event, got %s", stdout.String())
	}
	if drift["reason"] != "plan edit path is still pending approval" || drift["path"] != "b.txt" {
		t.Fatalf("unexpected drift event: %#v", drift)
	}
	if drift["handoff_id"] == "" || drift["approval_scope_id"] == "" {
		t.Fatalf("drift event should carry stable governance ids: %#v", drift)
	}
	handoff, ok := planExecuteEventByType(events, "idiom.plan_execute.executor_handoff_packet")
	if !ok {
		t.Fatalf("expected executor_handoff_packet event, got %s", stdout.String())
	}
	if handoff["approval_execution_permission"] != "execute_approved_scope" || handoff["approved_path_count"] != float64(1) || handoff["pending_path_count"] != float64(1) {
		t.Fatalf("handoff event should carry partial approval counters, got %#v", handoff)
	}
	body, err := os.ReadFile(filepath.Join(root, "b.txt"))
	if err != nil {
		t.Fatalf("read b.txt: %v", err)
	}
	if string(body) != "beta\n" {
		t.Fatalf("blocked drift should not mutate pending file, got %q", body)
	}
	for _, leaked := range []string{"raw_prompt", "customer-acme", "customer_acme", "token=", "/Users/ben/private"} {
		if strings.Contains(stdout.String(), leaked) {
			t.Fatalf("drift event leaked sensitive marker %q: %s", leaked, stdout.String())
		}
	}
}

func TestRunExecutorPhaseEmitsStalePacketHandoff(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("alpha\n"), 0o644); err != nil {
		t.Fatalf("seed a.txt: %v", err)
	}
	packID := "deadc0decafef00d"
	writePlanExecutePackArtifact(t, root, comprehension.PackResult{
		PackID:         packID,
		IndexID:        "idx-old",
		Root:           root,
		Task:           "stale packet",
		Mode:           "plan",
		Fresh:          false,
		CandidateFiles: []comprehension.PackFile{{Path: "a.txt", Role: "editable"}},
	})
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		PlanID:        "plan-stale-packet",
		PackID:        packID,
		Packet:        workspace.PlanArtifactPacket{PackID: packID, Status: "available", Freshness: "fresh"},
		Handoff:       workspace.PlanArtifactHandoff{ID: "handoff-1", Status: "ready", Ready: true},
		Edits:         []workspace.PlanEdit{{Path: "a.txt", OldText: "alpha", NewText: "ALPHA"}},
	}
	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}
	var stdout bytes.Buffer
	if _, err := runExecutorPhase(cfg, plan, nil, true, &stdout); err != nil {
		t.Fatalf("executor phase failed: %v", err)
	}
	events := planExecuteJSONEvents(t, stdout.String())
	handoff, ok := planExecuteEventByType(events, "idiom.plan_execute.executor_handoff_packet")
	if !ok {
		t.Fatalf("expected executor_handoff_packet event, got %s", stdout.String())
	}
	if handoff["packet_state"] != executorHandoffStateStale || handoff["reason"] != "legacy_artifact_fallback" || handoff["fresh"] != false {
		t.Fatalf("expected stale packet state, got %#v", handoff)
	}
	if _, ok := planExecuteEventByType(events, "idiom.plan_execute.executor_packet_stale"); !ok {
		t.Fatalf("expected executor_packet_stale compatibility event, got %s", stdout.String())
	}
}

func TestRunExecutorPhaseEmitsStaleGraphViewPacketWithoutLegacyArtifact(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/stalehandoff\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatalf("seed go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package stalehandoff\n\nfunc A() string { return \"alpha\" }\n"), 0o644); err != nil {
		t.Fatalf("seed a.go: %v", err)
	}
	build, err := comprehension.Build(t.Context(), comprehension.BuildOptions{Root: root, Language: "go"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	key := comprehension.ViewKey{IndexID: build.IndexID, Task: "stale graph packet", Mode: "plan", Query: comprehension.ViewQueryInputs{Path: "a.go"}}
	if err := comprehension.SaveView(root, key, comprehension.PackResult{
		PackID:         key.ID(),
		IndexID:        build.IndexID,
		Root:           root,
		Task:           "stale graph packet",
		Mode:           "plan",
		Fresh:          true,
		CandidateFiles: []comprehension.PackFile{{Path: "a.go", Role: "editable"}},
	}); err != nil {
		t.Fatalf("SaveView: %v", err)
	}
	if n, err := comprehension.MarkAllViewsStale(root); err != nil || n != 1 {
		t.Fatalf("MarkAllViewsStale n=%d err=%v", n, err)
	}
	if _, err := os.Stat(filepath.Join(root, ".idiom", "comprehension", "views", key.ID()+".json")); err == nil {
		t.Fatalf("legacy view artifact should not exist for graph-row stale test")
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat legacy view artifact: %v", err)
	}
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		PlanID:        "plan-stale-graph-row",
		PackID:        key.ID(),
		Packet:        workspace.PlanArtifactPacket{PackID: key.ID(), Status: "available", Freshness: "fresh"},
		Handoff:       workspace.PlanArtifactHandoff{ID: "handoff-1", Status: "ready", Ready: true},
		Edits:         []workspace.PlanEdit{{Path: "a.go", OldText: "alpha", NewText: "ALPHA"}},
	}
	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}
	var stdout bytes.Buffer
	if _, err := runExecutorPhase(cfg, plan, nil, true, &stdout); err != nil {
		t.Fatalf("executor phase failed: %v", err)
	}
	events := planExecuteJSONEvents(t, stdout.String())
	handoff, ok := planExecuteEventByType(events, "idiom.plan_execute.executor_handoff_packet")
	if !ok {
		t.Fatalf("expected executor_handoff_packet event, got %s", stdout.String())
	}
	if handoff["packet_state"] != executorHandoffStateStale || handoff["reason"] != "stale_graph_view" || handoff["fresh"] != false {
		t.Fatalf("expected stale graph-view packet state, got %#v", handoff)
	}
	if handoff["candidate_files"] != float64(1) {
		t.Fatalf("expected stale graph row to preserve candidate count, got %#v", handoff)
	}
	if _, ok := planExecuteEventByType(events, "idiom.plan_execute.executor_packet_stale"); !ok {
		t.Fatalf("expected executor_packet_stale compatibility event, got %s", stdout.String())
	}
}

func TestRunExecutorPhaseEmitsStaleForNonCurrentGraphViewPacketWithoutLegacyArtifact(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/noncurrenthandoff\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatalf("seed go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package noncurrenthandoff\n\nfunc A() string { return \"alpha\" }\n"), 0o644); err != nil {
		t.Fatalf("seed a.go: %v", err)
	}
	if _, err := comprehension.Build(t.Context(), comprehension.BuildOptions{Root: root, Language: "go"}); err != nil {
		t.Fatalf("Build: %v", err)
	}
	key := comprehension.ViewKey{IndexID: "old-index", Task: "non-current graph packet", Mode: "plan", Query: comprehension.ViewQueryInputs{Path: "a.go"}}
	if err := comprehension.SaveView(root, key, comprehension.PackResult{
		PackID:         key.ID(),
		IndexID:        "old-index",
		Root:           root,
		Task:           "non-current graph packet",
		Mode:           "plan",
		Fresh:          true,
		CandidateFiles: []comprehension.PackFile{{Path: "a.go", Role: "editable"}},
	}); err != nil {
		t.Fatalf("SaveView: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".idiom", "comprehension", "views", key.ID()+".json")); err == nil {
		t.Fatalf("legacy view artifact should not exist for non-current graph-row test")
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat legacy view artifact: %v", err)
	}
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		PlanID:        "plan-non-current-graph-row",
		PackID:        key.ID(),
		Packet:        workspace.PlanArtifactPacket{PackID: key.ID(), Status: "available", Freshness: "fresh"},
		Handoff:       workspace.PlanArtifactHandoff{ID: "handoff-1", Status: "ready", Ready: true},
		Edits:         []workspace.PlanEdit{{Path: "a.go", OldText: "alpha", NewText: "ALPHA"}},
	}
	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}
	var stdout bytes.Buffer
	if _, err := runExecutorPhase(cfg, plan, nil, true, &stdout); err != nil {
		t.Fatalf("executor phase failed: %v", err)
	}
	events := planExecuteJSONEvents(t, stdout.String())
	handoff, ok := planExecuteEventByType(events, "idiom.plan_execute.executor_handoff_packet")
	if !ok {
		t.Fatalf("expected executor_handoff_packet event, got %s", stdout.String())
	}
	if handoff["packet_state"] != executorHandoffStateStale || handoff["reason"] != "stale_graph_view" || handoff["fresh"] != false {
		t.Fatalf("expected stale non-current graph-view packet state, got %#v", handoff)
	}
	if _, ok := planExecuteEventByType(events, "idiom.plan_execute.executor_pack_miss"); ok {
		t.Fatalf("non-current graph row should not collapse to executor_pack_miss, got %s", stdout.String())
	}
}

func TestRunExecutorPhaseEmitsOverriddenPacketHandoffAndRedactsSensitiveIDs(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/override\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatalf("seed go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "pkg.go"), []byte("package override\n"), 0o644); err != nil {
		t.Fatalf("seed pkg.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("alpha\n"), 0o644); err != nil {
		t.Fatalf("seed a.txt: %v", err)
	}
	build, err := comprehension.Build(t.Context(), comprehension.BuildOptions{Root: root, Language: "go"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	key := comprehension.ViewKey{IndexID: build.IndexID, Task: "override packet", Mode: "plan"}
	packID := key.ID()
	if err := comprehension.SaveView(root, key, comprehension.PackResult{
		PackID:         packID,
		IndexID:        build.IndexID,
		Root:           root,
		Task:           "override packet",
		Mode:           "plan",
		Fresh:          true,
		CandidateFiles: []comprehension.PackFile{{Path: "a.txt", Role: "editable"}},
	}); err != nil {
		t.Fatalf("SaveView: %v", err)
	}
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		PlanID:        "plan raw_prompt customer-acme token=/Users/ben/private/client",
		PackID:        packID,
		Packet:        workspace.PlanArtifactPacket{PackID: "feedfacecafebeef", Status: "available", Freshness: "fresh"},
		Handoff:       workspace.PlanArtifactHandoff{ID: "handoff raw_prompt customer-acme token", Status: "ready", Ready: true},
		Edits:         []workspace.PlanEdit{{Path: "a.txt", OldText: "alpha", NewText: "ALPHA"}},
	}
	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}
	var stdout bytes.Buffer
	if _, err := runExecutorPhase(cfg, plan, nil, true, &stdout); err != nil {
		t.Fatalf("executor phase failed: %v", err)
	}
	events := planExecuteJSONEvents(t, stdout.String())
	handoff, ok := planExecuteEventByType(events, "idiom.plan_execute.executor_handoff_packet")
	if !ok {
		t.Fatalf("expected executor_handoff_packet event, got %s", stdout.String())
	}
	if handoff["packet_state"] != executorHandoffStateOverridden || handoff["reason"] != "packet_id_override" {
		t.Fatalf("expected overridden packet state, got %#v", handoff)
	}
	for _, leaked := range []string{"raw_prompt", "customer-acme", "customer_acme", "token=", "/Users/ben/private"} {
		if strings.Contains(stdout.String(), leaked) {
			t.Fatalf("handoff event leaked sensitive marker %q: %s", leaked, stdout.String())
		}
	}
}

func TestCollectPlanExecutePhaseEvidenceLoadsPacketAndExecutorChecks(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/evidence\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatalf("seed go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package pkg\n\nfunc A() string { return \"alpha\" }\n"), 0o644); err != nil {
		t.Fatalf("seed a.go: %v", err)
	}
	build, err := comprehension.Build(t.Context(), comprehension.BuildOptions{Root: root, Language: "go"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	key := comprehension.ViewKey{IndexID: build.IndexID, Task: "phase evidence", Mode: "plan"}
	cached := comprehension.PackResult{
		PackID:         key.ID(),
		IndexID:        build.IndexID,
		Root:           root,
		Task:           "phase evidence",
		Mode:           "plan",
		Fresh:          true,
		CandidateFiles: []comprehension.PackFile{{Path: "a.go", Role: "editable", Reason: "matched task"}},
		CandidateSymbols: []comprehension.PackSymbol{
			{ID: "symbol://a.go#A", QualifiedName: "A", Kind: "function", Path: "a.go", Reason: "matched task"},
		},
		Tests:          []comprehension.PackTest{{Command: "go test ./...", Path: "a.go", Reason: "packet suggested test"}},
		UnresolvedGaps: []string{"reason=stale_runtime_trace trace_id=t1"},
	}
	if err := comprehension.SaveView(root, key, cached); err != nil {
		t.Fatalf("SaveView: %v", err)
	}
	writePlanExecutePackArtifact(t, root, cached)
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package pkg\n\nfunc A() string { return \"beta\" }\n"), 0o644); err != nil {
		t.Fatalf("seed post-execute a.go: %v", err)
	}

	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		PackID:        key.ID(),
		Edits: []workspace.PlanEdit{
			{Path: "a.go", OldText: "alpha", NewText: "beta", Purpose: "change return marker"},
		},
	}
	evidence := collectPlanExecutePhaseEvidence(
		root,
		"review_repair",
		plan,
		[]string{"a.go"},
		[]string{"a.go"},
		[]string{"a.go"},
		reviewRepairEvidence{DiffCheckExit: 0},
		executorPhaseReport{SourceSanity: []phaseEvidenceCheck{{Kind: "source_sanity", Path: "a.go", Status: "ok", Summary: "Source sanity passed"}}},
	)

	if evidence.SchemaVersion != phaseEvidenceSchemaVersion {
		t.Fatalf("schema version = %q", evidence.SchemaVersion)
	}
	if evidence.Packet.Status != "available_stale" || evidence.Packet.PackID != key.ID() {
		t.Fatalf("expected stale packet artifact %s, got %#v", key.ID(), evidence.Packet)
	}
	if evidence.ExecutorHandoff.PacketState != executorHandoffStateStale || evidence.ExecutorHandoff.Reason == "" {
		t.Fatalf("expected stale executor handoff evidence, got %#v", evidence.ExecutorHandoff)
	}
	if len(evidence.Packet.CandidateFiles) != 1 || evidence.Packet.CandidateFiles[0].Path != "a.go" {
		t.Fatalf("candidate files missing from phase evidence: %#v", evidence.Packet.CandidateFiles)
	}
	if len(evidence.SuggestedTests) != 1 || evidence.SuggestedTests[0].Command != "go test ./..." {
		t.Fatalf("suggested tests missing from phase evidence: %#v", evidence.SuggestedTests)
	}
	if len(evidence.SourceSanity) != 1 || evidence.SourceSanity[0].Status != "ok" {
		t.Fatalf("source sanity missing from phase evidence: %#v", evidence.SourceSanity)
	}
	if len(evidence.SubmittedPlan) != 1 || evidence.SubmittedPlan[0].Status != "applied" {
		t.Fatalf("submitted plan state should show applied edit, got %#v", evidence.SubmittedPlan)
	}
	if !slicesContainString(evidence.UnresolvedGaps, "reason=stale_runtime_trace trace_id=t1") {
		t.Fatalf("packet gap should remain visible in unresolved gaps, got %#v", evidence.UnresolvedGaps)
	}
	if slicesContainString(evidence.OpenObligations, "packet_gap:reason=stale_runtime_trace trace_id=t1") {
		t.Fatalf("informational packet gaps must not become repair obligations, got %#v", evidence.OpenObligations)
	}
	telemetry := phaseEvidenceTelemetry(evidence)
	candidatePaths, ok := telemetry["candidate_file_paths"].([]string)
	if !ok || !slicesContainString(candidatePaths, "a.go") {
		t.Fatalf("telemetry should include bounded candidate paths, got %#v", telemetry["candidate_file_paths"])
	}
	testCommands, ok := telemetry["suggested_test_commands"].([]string)
	if !ok || !slicesContainString(testCommands, "go test ./...") {
		t.Fatalf("telemetry should include suggested test commands, got %#v", telemetry["suggested_test_commands"])
	}
}

func TestCollectPlanExecutePhaseEvidenceReportsEvidenceGaps(t *testing.T) {
	evidence := collectPlanExecutePhaseEvidence(
		t.TempDir(),
		"review_repair",
		workspace.PlanArtifact{SchemaVersion: workspace.PlanArtifactSchemaVersion, PackID: "deadbeefdeadbeef"},
		[]string{"a.go", "b.go"},
		[]string{"a.go", "b.go"},
		[]string{"a.go", "b.go"},
		reviewRepairEvidence{DiffCheckExit: 128, DiffCheck: "fatal: not a git repository"},
		executorPhaseReport{SourceSanity: []phaseEvidenceCheck{{Kind: "source_sanity", Path: "a.go", Status: "ok"}}},
	)

	for _, want := range []string{
		"context_packet_unavailable:miss",
		"patch_hygiene_unavailable",
		"source_sanity_missing:b.go",
	} {
		if !slicesContainString(evidence.OpenObligations, want) {
			t.Fatalf("expected obligation %q, got %#v", want, evidence.OpenObligations)
		}
	}
	if slicesContainString(evidence.OpenObligations, "source_sanity_missing:a.go") {
		t.Fatalf("source_sanity evidence should satisfy a.go, got %#v", evidence.OpenObligations)
	}
}

func TestLoadPlanExecutePacketEvidenceRejectsInvalidPackID(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".idiom", "comprehension", "views"), 0o755); err != nil {
		t.Fatalf("mkdir views: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".idiom", "comprehension", "secret.json"), []byte(`{"pack_id":"secret"}`), 0o644); err != nil {
		t.Fatalf("seed escaped-looking artifact: %v", err)
	}
	got := loadPlanExecutePacketEvidence(root, "../secret")
	if got.Status != "invalid_pack_id" {
		t.Fatalf("invalid pack ids must be rejected before cache-path fallback, got %#v", got)
	}
}

func TestPlanExecuteReviewRepairPromptRequiresBehavioralChecklist(t *testing.T) {
	prompt := planExecuteReviewRepairPrompt(
		"Implement the required behavior.",
		workspace.PlanArtifact{
			Rationale: "change behavior",
			Edits: []workspace.PlanEdit{
				{Path: "a.go", Purpose: "update behavior"},
			},
		},
		[]string{"a.go"},
		[]string{"a.go"},
		reviewRepairEvidence{
			Diff:          "diff --git a/a.go b/a.go\n",
			DiffCheckExit: 0,
			PhaseEvidence: phaseEvidenceBundle{
				SchemaVersion: phaseEvidenceSchemaVersion,
				Phase:         "review_repair",
				ChangedPaths:  []string{"a.go"},
				Packet: phaseEvidencePacket{
					Status:         "available",
					PackID:         "abc123",
					CandidateFiles: []phaseEvidenceFile{{Path: "a.go", Role: "editable", Reason: "matched task"}},
				},
				SuggestedTests: []phaseEvidenceTest{{Command: "go test ./...", Path: "a.go", Reason: "packet suggested test"}},
				SubmittedPlan:  []phaseEvidenceCheck{{Kind: "submitted_edit", Path: "a.go", Status: "applied", Summary: "edit 1/1: submitted new_text is present"}},
				SourceSanity:   []phaseEvidenceCheck{{Kind: "source_sanity", Path: "a.go", Status: "ok", Summary: "Source sanity passed"}},
			},
		},
	)
	for _, want := range []string{
		"Verification Checklist",
		"primary evidence ledger",
		"`context_status` (`refresh=true`)",
		"`context_query`",
		"`context_zoom`",
		"`code_navigate`",
		"Do not sweep large files",
		"one bounded `read_file` range",
		"behavior, not just literal code correctness",
		"Default/non-regression behavior",
		"Data, lifecycle, state, persistence, concurrency/threading, and performance",
		"Allowed Repair Paths",
		"Phase Evidence Bundle",
		"context_packet: status=available pack_id=abc123",
		"executor_handoff_packet",
		"submitted_plan_state",
		"do not re-submit",
		"`go test ./...`",
		"source_sanity",
		"tautology",
		"_assert_true(true)",
		"Decision: pass",
		"submit_plan.rationale",
		"executable final content",
		"not review prose",
		"`insert_after_line`+`content` only for small inline insertions after an existing line",
		"line-range `content` for non-trivial source/test replacements and large additions",
		"not a quoted JSON string",
		"Empty corrective plans are invalid",
		"pending outside this lane unless Phase Evidence names the exact command as successful",
		"Do not call `ask`",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("review-repair prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestPlanExecuteReviewRepairPromptTreatsFailedSourceSanityAsTerminalEvidence(t *testing.T) {
	prompt := planExecuteReviewRepairPrompt(
		"Implement the required behavior.",
		workspace.PlanArtifact{
			Rationale: "change behavior",
			Edits: []workspace.PlanEdit{
				{Path: "a.go", Purpose: "update behavior"},
			},
		},
		[]string{"a.go"},
		[]string{"a.go"},
		reviewRepairEvidence{
			Diff:          "diff --git a/a.go b/a.go\n",
			DiffCheckExit: 0,
			PhaseEvidence: phaseEvidenceBundle{
				SchemaVersion:   phaseEvidenceSchemaVersion,
				Phase:           "review_repair",
				ChangedPaths:    []string{"a.go"},
				OpenObligations: []string{"source_sanity_failed:a.go"},
				SubmittedPlan:   []phaseEvidenceCheck{{Kind: "submitted_edit", Path: "a.go", Status: "applied", Summary: "edit 1/1: submitted new_text is present"}},
				SourceSanity:    []phaseEvidenceCheck{{Kind: "source_sanity", Path: "a.go", Status: "failed", Summary: "Source sanity failed", Error: "compile failed"}},
			},
		},
	)
	for _, want := range []string{
		"failed verification or source_sanity obligations",
		"Treat that failure as sufficient post-edit evidence",
		"do not perform additional reads, searches, or context refreshes",
		"Submit a corrective plan",
		"<<QUESTION>>",
		"source_sanity_failed:a.go",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("review-repair failed-source-sanity prompt missing %q:\n%s", want, prompt)
		}
	}
	for _, forbidden := range []string{
		"If exact post-edit evidence is missing, make one targeted probe",
		"`context_status` (`refresh=true`)",
		"`context_query`",
		"`context_zoom`",
		"`code_navigate`",
		"one bounded `read_file` range",
	} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("failed-source-sanity prompt must not invite discovery via %q:\n%s", forbidden, prompt)
		}
	}
}

func TestPlanExecuteReviewRepairPromptQuotesOriginalSpecAllowlist(t *testing.T) {
	original := `# Atom

## Files To Modify

| File | Change |
|------|--------|
| ` + "`src/a.go`" + ` | MODIFY: original implementation target |
`
	prompt := planExecuteReviewRepairPrompt(
		original,
		workspace.PlanArtifact{
			Rationale: "change behavior",
			Edits: []workspace.PlanEdit{
				{Path: "src/a.go", Purpose: "update behavior"},
			},
		},
		[]string{"src/a.go"},
		[]string{"src/a.go"},
		reviewRepairEvidence{Diff: "diff --git a/src/a.go b/src/a.go\n", DiffCheckExit: 0},
	)
	if paths := app.RequiredEditPathsFromPrompt(prompt); len(paths) != 0 {
		t.Fatalf("original spec allowlist should be quoted reference text, got active required paths %#v\nprompt:\n%s", paths, prompt)
	}
	if !strings.Contains(prompt, "> ## Files To Modify") {
		t.Fatalf("original spec should remain visible as quoted reference text:\n%s", prompt)
	}
}

func TestCollectReviewRepairEvidenceUsesExecutorBaselineNotGitHead(t *testing.T) {
	root := initGitRepoForStaleness(t)
	gitCommit(t, root, "a.txt", "head\n", "initial")
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("dirty-before-lane\n"), 0o644); err != nil {
		t.Fatalf("seed dirty worktree: %v", err)
	}
	baseline := captureReviewRepairBaseline(root, []string{"a.txt"})
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("after-executor\n"), 0o644); err != nil {
		t.Fatalf("seed executor result: %v", err)
	}
	evidence := collectReviewRepairEvidence(root, []string{"a.txt"}, &baseline)
	if !strings.Contains(evidence.Diff, "-dirty-before-lane") || !strings.Contains(evidence.Diff, "+after-executor") {
		t.Fatalf("current-run diff should compare against executor baseline, got:\n%s", evidence.Diff)
	}
	if strings.Contains(evidence.Diff, "-head") {
		t.Fatalf("current-run diff must not compare against git HEAD, got:\n%s", evidence.Diff)
	}
}

func TestCollectReviewRepairEvidenceChecksCurrentRunWhitespace(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("clean-before\n"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	baseline := captureReviewRepairBaseline(root, []string{"a.txt"})
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("clean-before\nbad   \n"), 0o644); err != nil {
		t.Fatalf("seed executor result: %v", err)
	}
	evidence := collectReviewRepairEvidence(root, []string{"a.txt"}, &baseline)
	if evidence.DiffCheckExit != 1 {
		t.Fatalf("trailing whitespace should fail current-run hygiene, got exit %d diff=%s", evidence.DiffCheckExit, evidence.Diff)
	}
	if !strings.Contains(evidence.DiffCheck, "a.txt:2") {
		t.Fatalf("whitespace finding should include current-run path and line, got:\n%s", evidence.DiffCheck)
	}
}

func TestAutoRepairReviewPatchHygieneTrimsOnlyCurrentRunLines(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("preexisting   \nclean-before\n"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	baseline := captureReviewRepairBaseline(root, []string{"a.txt"})
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("preexisting   \nclean-before\nbad   \n"), 0o644); err != nil {
		t.Fatalf("seed executor result: %v", err)
	}
	evidence := collectReviewRepairEvidence(root, []string{"a.txt"}, &baseline)
	report, err := autoRepairReviewPatchHygiene(root, []string{"a.txt"}, evidence)
	if err != nil {
		t.Fatalf("auto repair patch hygiene: %v", err)
	}
	if !report.Repaired || report.Lines != 1 {
		t.Fatalf("expected one repaired line, got %#v", report)
	}
	got, err := os.ReadFile(filepath.Join(root, "a.txt"))
	if err != nil {
		t.Fatalf("read repaired file: %v", err)
	}
	if string(got) != "preexisting   \nclean-before\nbad\n" {
		t.Fatalf("auto repair should trim only current-run trailing whitespace, got %q", string(got))
	}
}

func TestRunReviewRepairPhaseAutoRepairsPatchHygieneBeforePrompt(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("clean-before\n"), 0o644); err != nil {
		t.Fatalf("seed a.txt: %v", err)
	}
	baseline := captureReviewRepairBaseline(root, []string{"a.txt"})
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("clean-before\nbad   \n"), 0o644); err != nil {
		t.Fatalf("seed executor result: %v", err)
	}
	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{Path: "a.txt", OldText: "clean-before\n", NewText: "clean-before\nbad   \n", Purpose: "seed changed path"},
		},
	}
	var stdout bytes.Buffer
	var gotPrompt string
	report, err := runReviewRepairPhase(
		cfg,
		"Add bad line.",
		plan,
		nil,
		&baseline,
		executorPhaseReport{},
		planExecuteOptions{},
		&stdout,
		func(_ config.Config, prompt string, _ cmdruntime.RunOptions, _ io.Writer) (string, error) {
			gotPrompt = prompt
			return "Decision: pass\n\nVerification Checklist:\n- Required behavior: bad line is present.\n- Patch hygiene: clean after mechanical repair.", nil
		},
		nil,
	)
	if err != nil {
		t.Fatalf("review-repair should accept auto-repaired hygiene: %v", err)
	}
	if report.AutoRepairs != 1 {
		t.Fatalf("expected one mechanical repair, got %#v", report)
	}
	if !strings.Contains(gotPrompt, "patch_hygiene: status=passed") {
		t.Fatalf("review prompt should receive repaired patch hygiene evidence, got:\n%s", gotPrompt)
	}
	if strings.Contains(gotPrompt, "submitted_edit_pending") || strings.Contains(gotPrompt, "submitted_edit_unknown") {
		t.Fatalf("mechanical whitespace repair should not make submitted edit look pending:\n%s", gotPrompt)
	}
	got, err := os.ReadFile(filepath.Join(root, "a.txt"))
	if err != nil {
		t.Fatalf("read repaired file: %v", err)
	}
	if string(got) != "clean-before\nbad\n" {
		t.Fatalf("expected repaired file, got %q", string(got))
	}
}

func TestRunReviewRepairPhaseRejectsPassWithoutChecklist(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("bad\n"), 0o644); err != nil {
		t.Fatalf("seed a.txt: %v", err)
	}
	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{Path: "a.txt", OldText: "old", NewText: "bad", Purpose: "seed changed path"},
		},
	}
	var stdout bytes.Buffer
	_, err := runReviewRepairPhase(
		cfg,
		"Review behavior.",
		plan,
		nil,
		nil,
		executorPhaseReport{},
		planExecuteOptions{},
		&stdout,
		func(_ config.Config, _ string, _ cmdruntime.RunOptions, _ io.Writer) (string, error) {
			return "Decision: pass\nNo issues.", nil
		},
		nil,
	)
	if err == nil {
		t.Fatalf("expected inconclusive review without checklist")
	}
	if !strings.Contains(err.Error(), "Verification Checklist") {
		t.Fatalf("error should require checklist, got %v", err)
	}
}

func TestRunReviewRepairPhaseRejectsRecoveryTemplateAsPass(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("good\n"), 0o644); err != nil {
		t.Fatalf("seed a.txt: %v", err)
	}
	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{Path: "a.txt", OldText: "bad", NewText: "good", Purpose: "seed changed path"},
		},
	}
	var stdout bytes.Buffer
	_, err := runReviewRepairPhase(
		cfg,
		"Review behavior.",
		plan,
		nil,
		nil,
		executorPhaseReport{},
		planExecuteOptions{},
		&stdout,
		func(_ config.Config, _ string, _ cmdruntime.RunOptions, _ io.Writer) (string, error) {
			return "The completion gate rejected the review-repair response.\n\nPass shape:\nDecision: pass\n\nVerification Checklist:\n- Required behavior: <checked fact>.\n\nDo not add prose before or after the chosen terminal shape.", nil
		},
		nil,
	)
	if err == nil {
		t.Fatalf("expected recovery template text not to count as a review pass")
	}
	if !strings.Contains(err.Error(), "Decision: pass") || !strings.Contains(err.Error(), "Verification Checklist") {
		t.Fatalf("error should require an actual terminal pass shape, got %v", err)
	}
}

func TestRunReviewRepairPhaseRejectsContradictoryPass(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("partial\n"), 0o644); err != nil {
		t.Fatalf("seed a.txt: %v", err)
	}
	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{Path: "a.txt", OldText: "old", NewText: "partial", Purpose: "seed changed path"},
		},
	}
	var stdout bytes.Buffer
	_, err := runReviewRepairPhase(
		cfg,
		"Review behavior.",
		plan,
		nil,
		nil,
		executorPhaseReport{},
		planExecuteOptions{
			JSONEvents: true,
			EditSetObligations: planexecute.EditSetObligations{}.With(
				planexecute.EditSetObligationKindTestEdit,
				"test",
			),
		},
		&stdout,
		func(_ config.Config, _ string, _ cmdruntime.RunOptions, _ io.Writer) (string, error) {
			return strings.Join([]string{
				"Verification Checklist",
				"- Required behavior: the implementation appears to be missing the actual application logic.",
				"",
				"Decision: pass",
			}, "\n"), nil
		},
		nil,
	)
	if err == nil {
		t.Fatalf("expected contradictory pass rejection")
	}
	if !strings.Contains(err.Error(), "contradictory") || !strings.Contains(err.Error(), "unresolved_failure_claim") {
		t.Fatalf("expected contradiction reason, got %v", err)
	}
	events := planExecuteJSONEvents(t, stdout.String())
	if _, ok := planExecuteEventByType(events, "idiom.plan_execute.review_repair_pass_rejected"); !ok {
		t.Fatalf("expected review_repair_pass_rejected event, got %s", stdout.String())
	}
}

func TestRunReviewRepairPhaseRejectsPassThatAdmitsCommandWasNotRun(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("after\n"), 0o644); err != nil {
		t.Fatalf("seed a.txt: %v", err)
	}
	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{Path: "a.txt", OldText: "before", NewText: "after", Purpose: "seed changed path"},
		},
	}
	var stdout bytes.Buffer
	_, err := runReviewRepairPhase(
		cfg,
		"Implement behavior and run focused tests.",
		plan,
		nil,
		nil,
		executorPhaseReport{},
		planExecuteOptions{JSONEvents: true},
		&stdout,
		func(_ config.Config, _ string, _ cmdruntime.RunOptions, _ io.Writer) (string, error) {
			return "Decision: pass\n\nVerification Checklist:\n- Required behavior: diff looks scoped.\n- Test or command evidence: We didn't run `go test ./cmd/idiom`.", nil
		},
		nil,
	)
	if err == nil {
		t.Fatalf("expected missing command evidence admission to reject review pass")
	}
	if !strings.Contains(err.Error(), "missing_command_evidence_claim") {
		t.Fatalf("expected missing command evidence rejection, got %v", err)
	}
	events := planExecuteJSONEvents(t, stdout.String())
	event, ok := planExecuteEventByType(events, "idiom.plan_execute.review_repair_pass_rejected")
	if !ok {
		t.Fatalf("expected review_repair_pass_rejected event, got %s", stdout.String())
	}
	if event["reason"] != "missing_command_evidence_claim" {
		t.Fatalf("expected missing_command_evidence_claim reason, got %#v", event)
	}
}

func TestRunReviewRepairPhaseRejectsPassWhenRequestedTestEditMissing(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "pkg.go"), []byte("package demo\n\nfunc Hardened() bool { return true }\n"), 0o644); err != nil {
		t.Fatalf("seed pkg.go: %v", err)
	}
	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{Path: "pkg.go", OldText: "return false", NewText: "return true", Purpose: "harden behavior"},
		},
	}
	var stdout bytes.Buffer
	var gotPrompt string
	_, err := runReviewRepairPhase(
		cfg,
		"Implement the hardening and add regression tests proving the edge case is rejected.",
		plan,
		nil,
		nil,
		executorPhaseReport{},
		planExecuteOptions{
			JSONEvents: true,
			EditSetObligations: planexecute.EditSetObligations{}.With(
				planexecute.EditSetObligationKindTestEdit,
				"test",
			),
		},
		&stdout,
		func(_ config.Config, prompt string, _ cmdruntime.RunOptions, _ io.Writer) (string, error) {
			gotPrompt = prompt
			return "Decision: pass\n\nVerification Checklist:\n- Required behavior: source diff contains the hardening.\n- Test or command evidence: diff evidence shows the code change.", nil
		},
		nil,
	)
	if err == nil {
		t.Fatalf("expected missing requested test edit to reject review pass")
	}
	if !strings.Contains(err.Error(), "missing_requested_test_edit") {
		t.Fatalf("expected missing test edit rejection, got %v", err)
	}
	if !strings.Contains(gotPrompt, "requested_test_edit_missing") {
		t.Fatalf("review prompt should expose requested test edit obligation, got:\n%s", gotPrompt)
	}
	events := planExecuteJSONEvents(t, stdout.String())
	event, ok := planExecuteEventByType(events, "idiom.plan_execute.review_repair_pass_rejected")
	if !ok {
		t.Fatalf("expected review_repair_pass_rejected event, got %s", stdout.String())
	}
	if event["reason"] != "missing_requested_test_edit" {
		t.Fatalf("expected missing_requested_test_edit reason, got %#v", event)
	}
}

func TestRunReviewRepairPhaseRejectsPassWhenRequestedImplementationEditMissing(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "feature_test.go"), []byte("package demo\n\nfunc TestEnabled(t *testing.T) {\n\tif !Enabled() {\n\t\tt.Fatal(\"disabled\")\n\t}\n}\n"), 0o644); err != nil {
		t.Fatalf("seed feature_test.go: %v", err)
	}
	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{Path: "feature_test.go", OldText: "func TestEnabled(t *testing.T) {}\n", NewText: "func TestEnabled(t *testing.T) {\n\tif !Enabled() {\n\t\tt.Fatal(\"disabled\")\n\t}\n}\n", Purpose: "add feature test"},
		},
	}
	var stdout bytes.Buffer
	var gotPrompt string
	_, err := runReviewRepairPhase(
		cfg,
		"Implement the feature. Start by adding failing tests, then implement.",
		plan,
		nil,
		nil,
		executorPhaseReport{},
		planExecuteOptions{
			JSONEvents: true,
			EditSetObligations: planexecute.EditSetObligations{}.With(
				planexecute.EditSetObligationKindImplementationEdit,
				"test",
			),
		},
		&stdout,
		func(_ config.Config, prompt string, _ cmdruntime.RunOptions, _ io.Writer) (string, error) {
			gotPrompt = prompt
			return "Decision: pass\n\nVerification Checklist:\n- Required behavior: test coverage was added.\n- Scope safety: diff is limited to one test file.", nil
		},
		nil,
	)
	if err == nil {
		t.Fatalf("expected missing requested implementation edit to reject review pass")
	}
	if !strings.Contains(err.Error(), "missing_requested_implementation_edit") {
		t.Fatalf("expected missing implementation edit rejection, got %v", err)
	}
	if !strings.Contains(gotPrompt, "requested_implementation_edit_missing") {
		t.Fatalf("review prompt should expose requested implementation edit obligation, got:\n%s", gotPrompt)
	}
	events := planExecuteJSONEvents(t, stdout.String())
	event, ok := planExecuteEventByType(events, "idiom.plan_execute.review_repair_pass_rejected")
	if !ok {
		t.Fatalf("expected review_repair_pass_rejected event, got %s", stdout.String())
	}
	if event["reason"] != "missing_requested_implementation_edit" {
		t.Fatalf("expected missing_requested_implementation_edit reason, got %#v", event)
	}
}

func TestRunReviewRepairPhaseRejectsPassWhenVerificationFailed(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "pkg.go"), []byte("package demo\n\nfunc Value() int { return 2 }\n"), 0o644); err != nil {
		t.Fatalf("seed pkg.go: %v", err)
	}
	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{Path: "pkg.go", OldText: "return 1", NewText: "return 2", Purpose: "change behavior"},
		},
	}
	command := "go test ./... -run 'TestValue' -count=1"
	var stdout bytes.Buffer
	var gotPrompt string
	_, err := runReviewRepairPhase(
		cfg,
		"Update Value. Make this pass: `"+command+"`.",
		plan,
		nil,
		nil,
		executorPhaseReport{Total: 1, Succeeded: 1, Verification: []phaseEvidenceCheck{{
			Kind:    "verification",
			Command: command,
			Status:  "failed",
			Error:   "TestValue failed",
		}}},
		planExecuteOptions{JSONEvents: true},
		&stdout,
		func(_ config.Config, prompt string, _ cmdruntime.RunOptions, _ io.Writer) (string, error) {
			gotPrompt = prompt
			return "Decision: pass\n\nVerification Checklist:\n- Requested verification: acceptable.", nil
		},
		nil,
	)
	if err == nil {
		t.Fatalf("expected failed requested verification to reject review pass")
	}
	if !strings.Contains(err.Error(), "verification_failed") {
		t.Fatalf("expected verification_failed rejection, got %v", err)
	}
	if !strings.Contains(gotPrompt, "verification_failed:"+command) {
		t.Fatalf("review prompt should expose failed verification obligation, got:\n%s", gotPrompt)
	}
	events := planExecuteJSONEvents(t, stdout.String())
	event, ok := planExecuteEventByType(events, "idiom.plan_execute.review_repair_pass_rejected")
	if !ok {
		t.Fatalf("expected review_repair_pass_rejected event, got %s", stdout.String())
	}
	if event["reason"] != "verification_failed" {
		t.Fatalf("expected verification_failed reason, got %#v", event)
	}
}

func TestRunReviewRepairPhaseRetriesContradictoryPassIntoCorrectivePlan(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "a.txt")
	if err := os.WriteFile(path, []byte("value=bad\n"), 0o644); err != nil {
		t.Fatalf("seed a.txt: %v", err)
	}
	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{Path: "a.txt", OldText: "value=good", NewText: "value=bad", Purpose: "introduce bad value"},
		},
	}
	var stdout bytes.Buffer
	attempts := 0
	report, err := runReviewRepairPhase(
		cfg,
		"Keep a.txt good.",
		plan,
		nil,
		nil,
		executorPhaseReport{Total: 1, Succeeded: 1, Verification: []phaseEvidenceCheck{{
			Kind:    "verification",
			Command: "check a.txt",
			Status:  "failed",
			Error:   "value was bad",
		}}},
		planExecuteOptions{JSONEvents: true},
		&stdout,
		func(cfg config.Config, prompt string, opts cmdruntime.RunOptions, _ io.Writer) (string, error) {
			attempts++
			if opts.TaskFamily != agent.PlanThenExecuteReviewRepairTaskFamily {
				t.Fatalf("unexpected task family %q", opts.TaskFamily)
			}
			if attempts == 1 {
				if !strings.Contains(prompt, "verification_failed:check a.txt") {
					t.Fatalf("initial prompt should expose failed verification, got:\n%s", prompt)
				}
				return "Decision: pass\n\nVerification Checklist:\n- Required behavior: acceptable.\n- Test or command evidence: acceptable.", nil
			}
			if !strings.Contains(prompt, "Rejected Previous Review Outcome") || !strings.Contains(prompt, "`Decision: pass` is not an allowed terminal outcome") {
				t.Fatalf("retry prompt should forbid another pass, got:\n%s", prompt)
			}
			writePlanExecuteTestArtifact(t, cfg.Workspace.Root, cfg.Workspace.SessionID, workspace.PlanArtifact{
				SchemaVersion: workspace.PlanArtifactSchemaVersion,
				Created:       time.Now().UTC().Format(time.RFC3339Nano),
				SessionID:     cfg.Workspace.SessionID,
				Rationale:     "Verification Checklist:\n- Failing item: check a.txt reported value was bad.\n- Scope safety: edit only a.txt.",
				Edits: []workspace.PlanEdit{
					{Path: "a.txt", OldText: "value=bad", NewText: "value=good", Purpose: "repair failed verification"},
				},
			})
			return "submitted repair plan", nil
		},
		nil,
	)
	if err != nil {
		t.Fatalf("expected retry corrective plan to repair, got %v", err)
	}
	if attempts != 2 {
		t.Fatalf("expected one retry after contradictory pass, got %d attempts", attempts)
	}
	if report.Status != "repaired" || report.Repairs != 1 {
		t.Fatalf("expected repaired report, got %#v", report)
	}
	if got := readFileString(t, path); got != "value=good\n" {
		t.Fatalf("expected repair to apply, got %q", got)
	}
	events := planExecuteJSONEvents(t, stdout.String())
	if _, ok := planExecuteEventByType(events, "idiom.plan_execute.review_repair_retry"); !ok {
		t.Fatalf("expected review_repair_retry event, got %s", stdout.String())
	}
}

func TestRunReviewRepairPhaseRejectsCorrectivePlanWhenRequestedTestEditStillMissing(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "pkg.go"), []byte("package demo\n\nfunc Hardened() bool { return false }\n"), 0o644); err != nil {
		t.Fatalf("seed pkg.go: %v", err)
	}
	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{Path: "pkg.go", OldText: "return false", NewText: "return true", Purpose: "harden behavior"},
		},
	}
	var stdout bytes.Buffer
	_, err := runReviewRepairPhase(
		cfg,
		"Implement the hardening and add regression tests proving the edge case is rejected.",
		plan,
		nil,
		nil,
		executorPhaseReport{},
		planExecuteOptions{
			JSONEvents:         true,
			EditSetObligations: planexecute.EditSetObligations{}.With(planexecute.EditSetObligationKindTestEdit, "test"),
		},
		&stdout,
		func(cfg config.Config, _ string, opts cmdruntime.RunOptions, _ io.Writer) (string, error) {
			if opts.TaskFamily != agent.PlanThenExecuteReviewRepairTaskFamily {
				t.Fatalf("unexpected task family %q", opts.TaskFamily)
			}
			writePlanExecuteTestArtifact(t, cfg.Workspace.Root, cfg.Workspace.SessionID, workspace.PlanArtifact{
				SchemaVersion: workspace.PlanArtifactSchemaVersion,
				Created:       time.Now().UTC().Format(time.RFC3339Nano),
				SessionID:     cfg.Workspace.SessionID,
				Rationale:     "Verification Checklist:\n- Required behavior: hardening is present.\n",
				Edits: []workspace.PlanEdit{
					{Path: "pkg.go", OldText: "return false", NewText: "return true", Purpose: "retry production-only hardening"},
				},
			})
			return "submitted production-only repair", nil
		},
		nil,
	)
	if err == nil {
		t.Fatalf("expected production-only repair plan rejection")
	}
	if !strings.Contains(err.Error(), "missing_requested_test_edit") {
		t.Fatalf("expected missing_requested_test_edit, got %v", err)
	}
	if got := readFileString(t, filepath.Join(root, "pkg.go")); strings.Contains(got, "return true") {
		t.Fatalf("repair rejection must happen before writes, got:\n%s", got)
	}
	events := planExecuteJSONEvents(t, stdout.String())
	event, ok := planExecuteEventByType(events, "idiom.plan_execute.review_repair_plan_rejected")
	if !ok {
		t.Fatalf("expected review_repair_plan_rejected event, got %s", stdout.String())
	}
	if event["reason"] != "missing_requested_test_edit" {
		t.Fatalf("expected missing_requested_test_edit reason, got %#v", event)
	}
}

func TestReviewAnswerPassContradictionAllowsNegatedMissingCoverage(t *testing.T) {
	answer := strings.Join([]string{
		"Verification Checklist",
		"- Required behavior: implemented.",
		"- Scope safety: no missing test coverage and no unresolved blockers.",
		"- Non-regression: no issues found.",
		"",
		"Decision: pass",
	}, "\n")
	if reason := reviewAnswerPassContradictionReason(answer); reason != "" {
		t.Fatalf("clean negated checklist should not be contradictory, got %q", reason)
	}
}

func TestReviewAnswerPassContradictionAllowsPlannerOwnedPendingCommand(t *testing.T) {
	answer := strings.Join([]string{
		"Verification Checklist",
		"- [x] Required behavior: implemented from the diff evidence.",
		"- [ ] npm test — planner-owned, pending outside this lane. Not marked as run.",
		"",
		"Decision: pass",
	}, "\n")
	if reason := reviewAnswerPassContradictionReason(answer); reason != "" {
		t.Fatalf("planner-owned pending verification should not make pass contradictory, got %q", reason)
	}
}

func TestReviewAnswerPassContradictionRejectsUnverifiableRequiredEdit(t *testing.T) {
	answer := strings.Join([]string{
		"Verification Checklist",
		"- Required behavior: WIRE cap tests were added.",
		"",
		"The actual test content from Required Edit 2 was not visible in the diff evidence provided.",
		"We cannot verify that the full test content was properly added.",
		"",
		"Decision: pass",
	}, "\n")
	if reason := reviewAnswerPassContradictionReason(answer); reason == "" {
		t.Fatalf("review pass that admits missing evidence must be contradictory")
	}
}

func TestReviewAnswerPassContradictionRejectsPlaceholderChecklist(t *testing.T) {
	answer := strings.Join([]string{
		"Decision: pass",
		"",
		"Verification Checklist:",
		"- Required behavior: cite a checked fact from Phase/Diff Evidence.",
		"- Scope safety: cite a checked fact from changed paths and allowed repair paths.",
	}, "\n")
	if reason := reviewAnswerPassContradictionReason(answer); reason != "placeholder_checklist_item" {
		t.Fatalf("placeholder checklist should be contradictory, got %q", reason)
	}
}

func TestReviewAnswerPassContradictionRejectsProjectedVerificationClaims(t *testing.T) {
	answer := strings.Join([]string{
		"Verification Checklist",
		"- Required behavior: the new scenario file matches the requested body.",
		"- Smoke Test Execution: ./tests/run_scenarios.sh tests/scenarios/smoke/example.json should pass.",
		"- Git Diff Check: git diff --check should pass with no whitespace errors.",
		"",
		"Decision: pass",
	}, "\n")
	if reason := reviewAnswerPassContradictionReason(answer); reason != "unsupported_verification_projection" {
		t.Fatalf("projected verification should be contradictory, got %q", reason)
	}
}

func TestRunReviewRepairPhaseUsesReviewRepairTaskFamilyForPass(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("good\n"), 0o644); err != nil {
		t.Fatalf("seed a.txt: %v", err)
	}
	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{Path: "a.txt", OldText: "bad", NewText: "good", Purpose: "seed changed path"},
		},
	}
	var stdout bytes.Buffer
	var gotTaskFamily string
	var gotPrompt string
	var gotDisableThinking bool
	report, err := runReviewRepairPhase(
		cfg,
		"Review behavior.",
		plan,
		nil,
		nil,
		executorPhaseReport{},
		planExecuteOptions{},
		&stdout,
		func(_ config.Config, prompt string, opts cmdruntime.RunOptions, _ io.Writer) (string, error) {
			gotTaskFamily = opts.TaskFamily
			gotPrompt = prompt
			gotDisableThinking = opts.DisableThinking
			return "Decision: pass\n\nVerification Checklist:\n- Required behavior: good is present.\n- Scope safety: no corrective edit needed.", nil
		},
		nil,
	)
	if err != nil {
		t.Fatalf("review-repair pass should be accepted: %v", err)
	}
	if report.Status != "pass (no corrective edits)" {
		t.Fatalf("unexpected report: %#v", report)
	}
	if gotTaskFamily != agent.PlanThenExecuteReviewRepairTaskFamily {
		t.Fatalf("review-repair should use review-repair task family, got %q", gotTaskFamily)
	}
	if !gotDisableThinking {
		t.Fatal("review-repair must force visible no-thinking output for its terminal contract")
	}
	for _, want := range []string{
		"## Terminal Output Templates",
		"Do not add prose before or after the chosen shape",
		"Pass shape:",
		"Corrective plan shape:",
		"Blocker shape:",
	} {
		if !strings.Contains(gotPrompt, want) {
			t.Fatalf("review-repair prompt missing %q:\n%s", want, gotPrompt)
		}
	}
	if strings.Contains(gotPrompt, "The plan-submission capability must cover every brief-required edit once") {
		t.Fatalf("review-repair prompt must not include planner-phase submit_plan closeout addendum:\n%s", gotPrompt)
	}
}

func TestRunPlanExecuteMainResumeRerunsReviewRepairOnly(t *testing.T) {
	configPath := writePlanExecuteConfig(t)
	root := filepath.Dir(configPath)
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("old\n"), 0o644); err != nil {
		t.Fatalf("seed a.txt: %v", err)
	}
	prompt := "Make a.txt say new."
	var firstStdout, firstStderr bytes.Buffer
	plannerSessionID := ""

	code := RunMain(
		[]string{"-config", configPath, "-prompt", prompt},
		&firstStdout,
		&firstStderr,
		func(cfg config.Config, _ string, opts cmdruntime.RunOptions, _ io.Writer) (string, error) {
			switch opts.TaskFamily {
			case agent.PlanThenExecutePlannerTaskFamily:
				plannerSessionID = cfg.Workspace.SessionID
				writePlanExecuteTestArtifact(t, cfg.Workspace.Root, cfg.Workspace.SessionID, workspace.PlanArtifact{
					SchemaVersion: workspace.PlanArtifactSchemaVersion,
					Created:       time.Now().UTC().Format(time.RFC3339Nano),
					SessionID:     cfg.Workspace.SessionID,
					Rationale:     "change a.txt",
					Edits: []workspace.PlanEdit{
						{Path: "a.txt", OldText: "old\n", NewText: "new\n", Purpose: "apply requested text"},
					},
				})
				return "submitted plan", nil
			case agent.PlanThenExecuteReviewRepairTaskFamily:
				return "Decision: pass\nNo checklist.", nil
			default:
				t.Fatalf("unexpected task family %q", opts.TaskFamily)
				return "", nil
			}
		},
	)
	if code != 1 {
		t.Fatalf("first run should fail review checklist gate, got %d stdout=%s stderr=%s", code, firstStdout.String(), firstStderr.String())
	}
	state, _, err := planexecute.LoadProgressState(root, plannerSessionID)
	if err != nil {
		t.Fatalf("load progress state: %v", err)
	}
	if state.Phase != planexecute.ProgressPhaseReviewRepair || state.Status != planexecute.ProgressStatusFailed {
		t.Fatalf("expected failed review state, got phase=%s status=%s", state.Phase, state.Status)
	}

	var resumedStdout, resumedStderr bytes.Buffer
	reviewCalls := 0
	resumedPrompt := ""
	code = RunMain(
		[]string{"-config", configPath, "-prompt", prompt, "-resume", plannerSessionID},
		&resumedStdout,
		&resumedStderr,
		func(_ config.Config, prompt string, opts cmdruntime.RunOptions, _ io.Writer) (string, error) {
			if opts.TaskFamily != agent.PlanThenExecuteReviewRepairTaskFamily {
				t.Fatalf("resume should run review-repair only, got task family %q", opts.TaskFamily)
			}
			reviewCalls++
			resumedPrompt = prompt
			return "Decision: pass\n\nVerification Checklist:\n- Required behavior: a.txt changed from old to new.\n- Scope safety: no corrective edit needed.", nil
		},
	)
	if code != 0 {
		t.Fatalf("resume should pass review, got %d stdout=%s stderr=%s", code, resumedStdout.String(), resumedStderr.String())
	}
	if reviewCalls != 1 {
		t.Fatalf("resume should call review once, got %d", reviewCalls)
	}
	if !strings.Contains(resumedPrompt, "-old") || !strings.Contains(resumedPrompt, "+new") {
		t.Fatalf("resumed review prompt should restore executor baseline diff, got:\n%s", resumedPrompt)
	}
	if got := readFileString(t, filepath.Join(root, "a.txt")); got != "new\n" {
		t.Fatalf("resume should not reapply executor edits, got %q", got)
	}
}

func TestRunPlanExecuteMainResumeUsesPersistedTypedEditSetObligations(t *testing.T) {
	configPath := writePlanExecuteConfig(t)
	root := filepath.Dir(configPath)
	if err := os.WriteFile(filepath.Join(root, "feature_test.go"), []byte("package demo\n\nfunc TestFeature(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatalf("seed feature_test.go: %v", err)
	}
	prompt := "Add coverage for existing feature behavior."
	var firstStdout, firstStderr bytes.Buffer
	plannerSessionID := ""

	code := RunMain(
		[]string{"-config", configPath, "-prompt", prompt},
		&firstStdout,
		&firstStderr,
		func(cfg config.Config, _ string, opts cmdruntime.RunOptions, _ io.Writer) (string, error) {
			switch opts.TaskFamily {
			case agent.PlanThenExecutePlannerTaskFamily:
				plannerSessionID = cfg.Workspace.SessionID
				writePlanExecuteTestArtifact(t, cfg.Workspace.Root, cfg.Workspace.SessionID, workspace.PlanArtifact{
					SchemaVersion: workspace.PlanArtifactSchemaVersion,
					Created:       time.Now().UTC().Format(time.RFC3339Nano),
					SessionID:     cfg.Workspace.SessionID,
					Rationale:     "test-only coverage plan",
					Edits: []workspace.PlanEdit{
						{Path: "feature_test.go", OldText: "func TestFeature(t *testing.T) {}\n", NewText: "func TestFeature(t *testing.T) {\n\tt.Helper()\n}\n", Purpose: "add coverage detail"},
					},
				})
				return "submitted test-only plan", nil
			case agent.PlanThenExecuteReviewRepairTaskFamily:
				return "Decision: pass\nNo checklist.", nil
			default:
				t.Fatalf("unexpected task family %q", opts.TaskFamily)
				return "", nil
			}
		},
	)
	if code != 1 {
		t.Fatalf("first run should fail review checklist gate, got %d stdout=%s stderr=%s", code, firstStdout.String(), firstStderr.String())
	}
	state, _, err := planexecute.LoadProgressState(root, plannerSessionID)
	if err != nil {
		t.Fatalf("load progress state: %v", err)
	}
	state.EditSetObligations = planexecute.EditSetObligations{}.With(planexecute.EditSetObligationKindImplementationEdit, "persisted_retry")
	if _, err := planexecute.SaveProgressState(root, &state); err != nil {
		t.Fatalf("save progress state: %v", err)
	}

	var resumedStdout, resumedStderr bytes.Buffer
	resumedPrompt := ""
	code = RunMain(
		[]string{"-config", configPath, "-prompt", prompt, "-resume", plannerSessionID, "-json"},
		&resumedStdout,
		&resumedStderr,
		func(_ config.Config, prompt string, opts cmdruntime.RunOptions, _ io.Writer) (string, error) {
			if opts.TaskFamily != agent.PlanThenExecuteReviewRepairTaskFamily {
				t.Fatalf("resume should run review-repair only, got task family %q", opts.TaskFamily)
			}
			resumedPrompt = prompt
			return "Decision: pass\n\nVerification Checklist:\n- Required behavior: test coverage is present.\n- Scope safety: no corrective edit needed.", nil
		},
	)
	if code != 1 {
		t.Fatalf("resume should reject pass against persisted implementation obligation, got %d stdout=%s stderr=%s", code, resumedStdout.String(), resumedStderr.String())
	}
	if !strings.Contains(resumedStderr.String(), planexecute.EditSetFailureMissingRequestedImplementationEdit) {
		t.Fatalf("expected persisted implementation obligation rejection, stderr=%s", resumedStderr.String())
	}
	if !strings.Contains(resumedPrompt, "requested_implementation_edit_missing") {
		t.Fatalf("review prompt should expose persisted implementation obligation, got:\n%s", resumedPrompt)
	}
}

func TestRunPlanExecuteMainResumeRejectsMissingSession(t *testing.T) {
	configPath := writePlanExecuteConfig(t)
	var stdout, stderr bytes.Buffer

	code := RunMain(
		[]string{"-config", configPath, "-prompt", "resume missing", "-resume", "missing-session"},
		&stdout,
		&stderr,
		func(config.Config, string, cmdruntime.RunOptions, io.Writer) (string, error) {
			t.Fatal("prompt runner should not be called for missing resume state")
			return "", nil
		},
	)

	if code != 1 {
		t.Fatalf("expected missing resume to fail, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "does not exist") {
		t.Fatalf("stderr should explain missing resume state, got %s", stderr.String())
	}
}

func TestRunReviewRepairPhaseDegradesWhenReviewModelUnavailable(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("good\n"), 0o644); err != nil {
		t.Fatalf("seed a.txt: %v", err)
	}
	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{Path: "a.txt", OldText: "bad", NewText: "good", Purpose: "seed changed path"},
		},
	}
	var stdout bytes.Buffer
	report, err := runReviewRepairPhase(
		cfg,
		"Review behavior.",
		plan,
		nil,
		nil,
		executorPhaseReport{Succeeded: 1, Total: 1},
		planExecuteOptions{JSONEvents: true},
		&stdout,
		func(_ config.Config, _ string, _ cmdruntime.RunOptions, _ io.Writer) (string, error) {
			return "", fmt.Errorf("Post \"http://127.0.0.1:47781/v1/chat/completions\": EOF")
		},
		nil,
	)
	if err != nil {
		t.Fatalf("review model transport failure should degrade after executor success, got %v", err)
	}
	if report.Status != "degraded (review unavailable)" || report.ReasonClass != "backend_eof" {
		t.Fatalf("unexpected degraded review report: %#v", report)
	}
	events := planExecuteJSONEvents(t, stdout.String())
	ev, ok := planExecuteEventByType(events, "idiom.plan_execute.review_repair_degraded")
	if !ok {
		t.Fatalf("expected review_repair_degraded event, got %s", stdout.String())
	}
	if ev["reason_class"] != "backend_eof" || ev["status"] != "review_unavailable" {
		t.Fatalf("event should carry bounded unavailable reason, got %#v", ev)
	}
	if _, ok := planExecuteEventByType(events, "idiom.plan_execute.review_repair_failed"); ok {
		t.Fatalf("transport degradation must not be emitted as failed after executor success: %s", stdout.String())
	}
}

func TestRunReviewRepairPhaseAppliesChecklistRepairPlanWithinScope(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("bad\n"), 0o644); err != nil {
		t.Fatalf("seed a.txt: %v", err)
	}
	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{Path: "a.txt", OldText: "old", NewText: "bad", Purpose: "seed changed path"},
		},
	}
	var stdout bytes.Buffer
	report, err := runReviewRepairPhase(
		cfg,
		"Make a.txt say good.",
		plan,
		nil,
		nil,
		executorPhaseReport{},
		planExecuteOptions{},
		&stdout,
		func(cfg config.Config, _ string, _ cmdruntime.RunOptions, _ io.Writer) (string, error) {
			writePlanExecuteTestArtifact(t, cfg.Workspace.Root, cfg.Workspace.SessionID, workspace.PlanArtifact{
				SchemaVersion: workspace.PlanArtifactSchemaVersion,
				Created:       time.Now().UTC().Format(time.RFC3339Nano),
				SessionID:     cfg.Workspace.SessionID,
				Rationale:     "Verification Checklist:\n- Required behavior: a.txt must say good; current diff says bad.\n",
				Edits: []workspace.PlanEdit{
					{Path: "a.txt", OldText: "bad", NewText: "good", Purpose: "repair reviewed behavior"},
				},
			})
			return "submitted repair", nil
		},
		nil,
	)
	if err != nil {
		t.Fatalf("review-repair should apply scoped repair: %v", err)
	}
	if report.Status != "repaired" || report.Repairs != 1 {
		t.Fatalf("unexpected review report: %#v", report)
	}
	got, err := os.ReadFile(filepath.Join(root, "a.txt"))
	if err != nil {
		t.Fatalf("read a.txt: %v", err)
	}
	if string(got) != "good\n" {
		t.Fatalf("repair should update a.txt, got %q", string(got))
	}
}

func TestRunReviewRepairPhaseRejectsOutOfScopeRepairBeforeWrites(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("bad\n"), 0o644); err != nil {
		t.Fatalf("seed a.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "surprise.txt"), []byte("keep\n"), 0o644); err != nil {
		t.Fatalf("seed surprise.txt: %v", err)
	}
	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{Path: "a.txt", OldText: "old", NewText: "bad", Purpose: "seed changed path"},
		},
	}
	var stdout bytes.Buffer
	_, err := runReviewRepairPhase(
		cfg,
		"Review behavior.",
		plan,
		nil,
		nil,
		executorPhaseReport{},
		planExecuteOptions{},
		&stdout,
		func(cfg config.Config, _ string, _ cmdruntime.RunOptions, _ io.Writer) (string, error) {
			writePlanExecuteTestArtifact(t, cfg.Workspace.Root, cfg.Workspace.SessionID, workspace.PlanArtifact{
				SchemaVersion: workspace.PlanArtifactSchemaVersion,
				Created:       time.Now().UTC().Format(time.RFC3339Nano),
				SessionID:     cfg.Workspace.SessionID,
				Rationale:     "Verification Checklist:\n- Scope safety failed.\n",
				Edits: []workspace.PlanEdit{
					{Path: "surprise.txt", OldText: "keep", NewText: "mutated", Purpose: "out-of-scope repair"},
				},
			})
			return "submitted repair", nil
		},
		nil,
	)
	if err == nil {
		t.Fatalf("expected out-of-scope review repair rejection")
	}
	if !strings.Contains(err.Error(), "allowed_edit_paths") {
		t.Fatalf("expected allowed_edit_paths rejection, got %v", err)
	}
	got, err := os.ReadFile(filepath.Join(root, "surprise.txt"))
	if err != nil {
		t.Fatalf("read surprise.txt: %v", err)
	}
	if string(got) != "keep\n" {
		t.Fatalf("out-of-scope repair must not write surprise.txt, got %q", string(got))
	}
}

func TestRunReviewRepairPhaseRejectsNoOpAssertionRepairBeforeWrites(t *testing.T) {
	root := t.TempDir()
	original := "extends Node\n\nfunc test_case():\n\tpass\n"
	if err := os.WriteFile(filepath.Join(root, "suite.gd"), []byte(original), 0o644); err != nil {
		t.Fatalf("seed suite.gd: %v", err)
	}
	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{Path: "suite.gd", OldText: "\tpass\n", NewText: "\t# missing behavioral assertion\n\tpass\n", Purpose: "seed changed path"},
		},
	}
	var stdout bytes.Buffer
	_, err := runReviewRepairPhase(
		cfg,
		"Review behavior.",
		plan,
		nil,
		nil,
		executorPhaseReport{},
		planExecuteOptions{},
		&stdout,
		func(cfg config.Config, _ string, _ cmdruntime.RunOptions, _ io.Writer) (string, error) {
			writePlanExecuteTestArtifact(t, cfg.Workspace.Root, cfg.Workspace.SessionID, workspace.PlanArtifact{
				SchemaVersion: workspace.PlanArtifactSchemaVersion,
				Created:       time.Now().UTC().Format(time.RFC3339Nano),
				SessionID:     cfg.Workspace.SessionID,
				Rationale:     "Verification Checklist:\n- Required behavior still needs a concrete assertion.\n",
				Edits: []workspace.PlanEdit{
					{Path: "suite.gd", OldText: "\tpass\n", NewText: "\t_assert_true(true, \"contract validated\")\n", Purpose: "fake behavioral proof"},
				},
			})
			return "submitted repair", nil
		},
		nil,
	)
	if err == nil {
		t.Fatalf("expected no-op assertion repair rejection")
	}
	if !strings.Contains(err.Error(), "no-op assertion") {
		t.Fatalf("expected no-op assertion rejection, got %v", err)
	}
	got, readErr := os.ReadFile(filepath.Join(root, "suite.gd"))
	if readErr != nil {
		t.Fatalf("read suite.gd: %v", readErr)
	}
	if string(got) != original {
		t.Fatalf("review-repair preflight must reject before writes, got %q", string(got))
	}
}

func writePlanExecuteTestArtifact(t *testing.T, root string, sessionID string, artifact workspace.PlanArtifact) string {
	t.Helper()
	dir := filepath.Join(root, ".idiom", "plans")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir plans: %v", err)
	}
	if artifact.Created == "" {
		artifact.Created = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if artifact.SchemaVersion == "" {
		artifact.SchemaVersion = workspace.PlanArtifactSchemaVersion
	}
	if artifact.SchemaVersion == workspace.PlanArtifactSchemaVersion {
		if artifact.PlanID == "" {
			artifact.PlanID = "test-plan"
		}
		if artifact.Task.Summary == "" && artifact.Task.ID == "" {
			artifact.Task.Summary = "test plan-execute artifact"
		}
		if artifact.Contract.Kind == "" {
			artifact.Contract.Kind = "plan_then_execute"
		}
		if artifact.Contract.StrategyOwner == "" {
			artifact.Contract.StrategyOwner = "model"
		}
		if artifact.Contract.ContractOwner == "" {
			artifact.Contract.ContractOwner = "harness"
		}
		if artifact.Packet.Status == "" {
			if artifact.PackID != "" {
				artifact.Packet.Status = "provided"
				artifact.Packet.PackID = artifact.PackID
			} else {
				artifact.Packet.Status = "not_provided"
			}
		}
		if len(artifact.Paths.Editable) == 0 {
			artifact.Paths.Editable = planExecuteTestEditPaths(artifact.Edits)
		}
		if len(artifact.Paths.Changed) == 0 {
			artifact.Paths.Changed = planExecuteTestEditPaths(artifact.Edits)
		}
		if len(artifact.Evidence) == 0 {
			artifact.Evidence = []workspace.PlanArtifactEvidence{{Kind: "test_fixture", Summary: "bounded plan-execute test artifact"}}
		}
		if len(artifact.VerificationObligations) == 0 {
			artifact.VerificationObligations = []workspace.PlanArtifactVerificationObligation{{Kind: "test_lane_sanity", Status: "open", Required: true}}
		}
		if artifact.Approval.Status == "" {
			artifact.Approval.Status = "implicit"
		}
		if artifact.Handoff.Status == "" {
			artifact.Handoff.Status = "ready"
			artifact.Handoff.Ready = true
		}
	}
	artifact.SessionID = sessionID
	body, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		t.Fatalf("marshal plan artifact: %v", err)
	}
	path := filepath.Join(dir, time.Now().UTC().Format("20060102-150405000")+".plan.json")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write plan artifact: %v", err)
	}
	return path
}

func planExecuteTestEditPaths(edits []workspace.PlanEdit) []string {
	seen := map[string]struct{}{}
	var paths []string
	for _, edit := range edits {
		path := strings.TrimSpace(edit.Path)
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	return paths
}

func writePlanExecutePackArtifact(t *testing.T, root string, pack comprehension.PackResult) string {
	t.Helper()
	path := filepath.Join(root, ".idiom", "comprehension", "views", pack.PackID+".json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir pack artifact: %v", err)
	}
	body, err := json.MarshalIndent(pack, "", "  ")
	if err != nil {
		t.Fatalf("marshal pack artifact: %v", err)
	}
	if err := os.WriteFile(path, append(body, '\n'), 0o644); err != nil {
		t.Fatalf("write pack artifact: %v", err)
	}
	return path
}

func TestRunExecutorPhaseDispatchesEditsInOrder(t *testing.T) {
	// Seed two files, run the executor phase against a plan that
	// mutates each, and verify both writes landed.
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("alpha\n"), 0o644); err != nil {
		t.Fatalf("seed a.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "b.txt"), []byte("beta\n"), 0o644); err != nil {
		t.Fatalf("seed b.txt: %v", err)
	}
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Rationale:     "rename markers",
		Edits: []workspace.PlanEdit{
			{Path: "a.txt", OldText: "alpha", NewText: "ALPHA", Purpose: "uppercase a"},
			{Path: "b.txt", OldText: "beta", NewText: "BETA", Purpose: "uppercase b"},
		},
	}
	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}
	var stdout bytes.Buffer
	report, err := runExecutorPhase(cfg, plan, nil, false, &stdout)
	if err != nil {
		t.Fatalf("executor phase failed: %v", err)
	}
	if report.Total != 2 || report.Succeeded != 2 || report.Failed != 0 {
		t.Fatalf("unexpected report: %#v", report)
	}
	got, err := os.ReadFile(filepath.Join(root, "a.txt"))
	if err != nil {
		t.Fatalf("read a.txt: %v", err)
	}
	if !strings.Contains(string(got), "ALPHA") {
		t.Fatalf("a.txt should contain ALPHA after edit, got %q", string(got))
	}
	got, err = os.ReadFile(filepath.Join(root, "b.txt"))
	if err != nil {
		t.Fatalf("read b.txt: %v", err)
	}
	if !strings.Contains(string(got), "BETA") {
		t.Fatalf("b.txt should contain BETA after edit, got %q", string(got))
	}
}

func TestRunExecutorPhaseRunsSourceSanityAfterSourceEdits(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "suite.gd"), []byte("extends Node\n\nfunc helper():\n\tpass\n"), 0o644); err != nil {
		t.Fatalf("seed suite.gd: %v", err)
	}
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{Path: "suite.gd", OldText: "\tpass\n", NewText: "", Purpose: "remove helper body"},
		},
	}
	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}
	var stdout bytes.Buffer
	report, err := runExecutorPhase(cfg, plan, nil, true, &stdout)
	if err == nil {
		t.Fatalf("expected source_sanity failure after source edit")
	}
	if report.Succeeded != 1 || report.Failed != 0 {
		t.Fatalf("source_sanity runs after successful dispatch; unexpected report %#v", report)
	}
	if len(report.SourceSanity) != 1 || report.SourceSanity[0].Path != "suite.gd" || report.SourceSanity[0].Status != "failed" {
		t.Fatalf("executor report should retain failed source_sanity evidence, got %#v", report.SourceSanity)
	}
	if !strings.Contains(err.Error(), "source_sanity failed") {
		t.Fatalf("expected source_sanity error, got %v", err)
	}
	if !strings.Contains(stdout.String(), `idiom.plan_execute.source_sanity`) {
		t.Fatalf("expected source_sanity event, got %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), `"source_sanity_status":"failed"`) {
		t.Fatalf("expected failed source_sanity status in executor_complete, got %s", stdout.String())
	}
}

func TestRunExecutorPhaseRunsInferredTouchedGoPackageTests(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/inferred\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatalf("seed go.mod: %v", err)
	}
	pkgDir := filepath.Join(root, "pkg", "feature")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatalf("mkdir package: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "feature.go"), []byte("package feature\n\nfunc Value() int { return 1 }\n"), 0o644); err != nil {
		t.Fatalf("seed feature.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "feature_test.go"), []byte("package feature\n\nimport \"testing\"\n\nfunc TestValue(t *testing.T) {\n\tif Value() != 1 {\n\t\tt.Fatalf(\"unexpected value\")\n\t}\n}\n"), 0o644); err != nil {
		t.Fatalf("seed feature_test.go: %v", err)
	}
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{Path: "pkg/feature/feature.go", OldText: "return 1", NewText: "return 2", Purpose: "introduce a failing touched-package test"},
		},
	}
	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}
	var stdout bytes.Buffer
	report, err := runExecutorPhaseScopedWithVerification(cfg, plan, nil, nil, nil, true, &stdout)
	if err == nil {
		t.Fatalf("expected inferred touched-package verification failure")
	}
	if len(report.Verification) != 1 || report.Verification[0].Command != "go test ./pkg/feature" || report.Verification[0].Status != "failed" {
		t.Fatalf("expected failed inferred verification evidence, got %#v", report.Verification)
	}
	if !strings.Contains(err.Error(), "verification failed for \"go test ./pkg/feature\"") {
		t.Fatalf("expected explicit verification failure, got %v", err)
	}
	if !strings.Contains(stdout.String(), `"command":"go test ./pkg/feature"`) {
		t.Fatalf("expected verification event for inferred command, got %s", stdout.String())
	}
}

func TestRunExecutorPhaseRunsInferredNestedModulePackageTests(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.work"), []byte("go 1.22\n\nuse ./nested\n"), 0o644); err != nil {
		t.Fatalf("seed root go.work: %v", err)
	}
	moduleDir := filepath.Join(root, "nested")
	if err := os.MkdirAll(filepath.Join(moduleDir, "pkg", "feature"), 0o755); err != nil {
		t.Fatalf("mkdir nested package: %v", err)
	}
	if err := os.WriteFile(filepath.Join(moduleDir, "go.mod"), []byte("module example.com/nested\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatalf("seed nested go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(moduleDir, "pkg", "feature", "feature.go"), []byte("package feature\n\nfunc Value() int { return 1 }\n"), 0o644); err != nil {
		t.Fatalf("seed feature.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(moduleDir, "pkg", "feature", "feature_test.go"), []byte("package feature\n\nimport \"testing\"\n\nfunc TestValue(t *testing.T) {\n\tif Value() != 1 {\n\t\tt.Fatalf(\"nested module assertion ran: got %d\", Value())\n\t}\n}\n"), 0o644); err != nil {
		t.Fatalf("seed feature_test.go: %v", err)
	}
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{Path: "nested/pkg/feature/feature.go", OldText: "return 1", NewText: "return 2", Purpose: "introduce a failing nested touched-package test"},
		},
	}
	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}
	var stdout bytes.Buffer
	report, err := runExecutorPhaseScopedWithVerification(cfg, plan, nil, nil, nil, true, &stdout)
	if err == nil {
		t.Fatalf("expected inferred nested-module verification failure")
	}
	if len(report.Verification) != 1 {
		t.Fatalf("expected one inferred verification check, got %#v report=%#v err=%v stdout=%s", report.Verification, report, err, stdout.String())
	}
	check := report.Verification[0]
	if check.Command != "go test ./pkg/feature" || check.WorkingDir != "nested" || check.Status != "failed" {
		t.Fatalf("expected failed nested-module verification evidence, got %#v", check)
	}
	if !strings.Contains(check.Error, "nested module assertion ran") {
		t.Fatalf("expected nested module test output, got %#v", check)
	}
	if !strings.Contains(stdout.String(), `"working_dir":"nested"`) {
		t.Fatalf("expected verification event to include working_dir, got %s", stdout.String())
	}
}

func TestRunExecutorPhaseAbortsAtPreflightOnMissingFile(t *testing.T) {
	// Pre-execute plan validator catches edit 2's missing file before
	// any edit dispatches. This is by-design atomic-preflight: rather
	// than landing edit 1 then failing edit 2 (the old half-applied
	// state), the whole plan is rejected with a single error pointing
	// at the bad edit. Counts remain at 0 because no dispatch ran.
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("alpha\n"), 0o644); err != nil {
		t.Fatalf("seed a.txt: %v", err)
	}
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{Path: "a.txt", OldText: "alpha", NewText: "ALPHA"},
			{Path: "missing.txt", OldText: "x", NewText: "y"},
		},
	}
	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}
	var stdout bytes.Buffer
	report, err := runExecutorPhase(cfg, plan, nil, false, &stdout)
	if err == nil {
		t.Fatalf("expected preflight abort on missing file, got nil")
	}
	if report.Succeeded != 0 || report.Failed != 0 {
		t.Fatalf("preflight aborts before any dispatch — report should be untouched, got %#v", report)
	}
	if !strings.Contains(err.Error(), "missing.txt") {
		t.Fatalf("error should name the failing path, got %v", err)
	}
	if !strings.Contains(err.Error(), "preflight") {
		t.Fatalf("error should identify the abort layer as preflight, got %v", err)
	}
	// a.txt must NOT have been modified — atomic preflight means
	// nothing landed when one edit was bad.
	got, _ := os.ReadFile(filepath.Join(root, "a.txt"))
	if string(got) != "alpha\n" {
		t.Fatalf("preflight abort must leave files untouched, got %q", string(got))
	}
}

func TestRunExecutorPhaseRejectsPlanMissingRequiredEditPathBeforeWrites(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("alpha\n"), 0o644); err != nil {
		t.Fatalf("seed a.txt: %v", err)
	}
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{Path: "a.txt", OldText: "alpha", NewText: "ALPHA"},
		},
	}
	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}
	var stdout bytes.Buffer
	report, err := runExecutorPhase(cfg, plan, []string{"a.txt", "b.txt"}, true, &stdout)
	if err == nil {
		t.Fatalf("expected required_edit_paths preflight failure")
	}
	if report.Succeeded != 0 || report.Failed != 0 {
		t.Fatalf("required path preflight must abort before dispatch, got %#v", report)
	}
	if !strings.Contains(err.Error(), "b.txt") {
		t.Fatalf("error should name the missing required path, got %v", err)
	}
	if !strings.Contains(stdout.String(), `idiom.plan_execute.preflight_failed`) {
		t.Fatalf("expected preflight_failed event, got %s", stdout.String())
	}
	got, _ := os.ReadFile(filepath.Join(root, "a.txt"))
	if string(got) != "alpha\n" {
		t.Fatalf("preflight abort must leave files untouched, got %q", string(got))
	}
}

func TestRunExecutorPhaseRejectsPlanOutsideRequiredEditPathsBeforeWrites(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("alpha\n"), 0o644); err != nil {
		t.Fatalf("seed a.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "surprise.txt"), []byte("surprise\n"), 0o644); err != nil {
		t.Fatalf("seed surprise.txt: %v", err)
	}
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{Path: "a.txt", OldText: "alpha", NewText: "ALPHA"},
			{Path: "surprise.txt", OldText: "surprise", NewText: "SURPRISE"},
		},
	}
	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}
	var stdout bytes.Buffer
	report, err := runExecutorPhase(cfg, plan, []string{"a.txt"}, true, &stdout)
	if err == nil {
		t.Fatalf("expected outside required_edit_paths preflight failure")
	}
	if report.Succeeded != 0 || report.Failed != 0 {
		t.Fatalf("scope preflight must abort before dispatch, got %#v", report)
	}
	if !strings.Contains(err.Error(), "surprise.txt") {
		t.Fatalf("error should name the outside path, got %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(root, "surprise.txt"))
	if string(got) != "surprise\n" {
		t.Fatalf("preflight abort must leave outside file untouched, got %q", string(got))
	}
}

func TestPreflightPlanEditsCatchesAnchorMismatch(t *testing.T) {
	// The donkey-atom failure mode: plan's old_text spans content
	// that doesn't appear in the file in that exact form. Preflight
	// must catch it and name the edit's index + path.
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "x.txt"), []byte("line one\nline two\nline three\n"), 0o644); err != nil {
		t.Fatalf("seed x.txt: %v", err)
	}
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{Path: "x.txt", OldText: "line one\nline three", NewText: "X"},
		},
	}
	got := preflightPlanEdits(root, plan)
	if got == nil {
		t.Fatalf("preflight must catch anchor mismatch (combined non-adjacent lines)")
	}
	if got.Index != 0 {
		t.Fatalf("mismatch should point at edit 0, got index %d", got.Index)
	}
	if got.Path != "x.txt" {
		t.Fatalf("mismatch should name the path, got %q", got.Path)
	}
	if !strings.Contains(strings.ToLower(got.Reason), "old_text") {
		t.Fatalf("mismatch reason should mention old_text, got %q", got.Reason)
	}
}

func TestPreflightPlanEditsAcceptsValidPlan(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "x.txt"), []byte("hello\nworld\n"), 0o644); err != nil {
		t.Fatalf("seed x.txt: %v", err)
	}
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{Path: "x.txt", OldText: "hello", NewText: "HELLO"},
		},
	}
	if got := preflightPlanEdits(root, plan); got != nil {
		t.Fatalf("preflight must accept a clean plan, got mismatch %#v", got)
	}
}

func TestPreflightPlanEditsRejectsStaleAnchorProvenance(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "x.txt"), []byte("hello\nworld\n"), 0o644); err != nil {
		t.Fatalf("seed x.txt: %v", err)
	}
	edits, err := workspace.AttachPlanEditAnchorProvenance(root, []workspace.PlanEdit{
		{Path: "x.txt", OldText: "hello", NewText: "HELLO"},
	})
	if err != nil {
		t.Fatalf("attach anchor provenance: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "x.txt"), []byte("prefix\nhello\nworld\n"), 0o644); err != nil {
		t.Fatalf("mutate x.txt: %v", err)
	}
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits:         edits,
	}
	got := preflightPlanEdits(root, plan)
	if got == nil {
		t.Fatalf("preflight must reject stale anchor provenance before writes")
	}
	if got.Index != 0 || got.Path != "x.txt" {
		t.Fatalf("mismatch should point at edit 0 in x.txt, got %#v", got)
	}
	if !strings.Contains(got.Reason, "anchor provenance stale") || !strings.Contains(got.Reason, "file fingerprint changed") {
		t.Fatalf("mismatch should report stale provenance, got %q", got.Reason)
	}
}

func TestPreflightPlanEditsRejectsEscapingPathBeforeRead(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(filepath.Dir(root), "outside.txt")
	if err := os.WriteFile(outside, []byte("secret\n"), 0o644); err != nil {
		t.Fatalf("seed outside file: %v", err)
	}
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{Path: "../outside.txt", OldText: "secret", NewText: "redacted"},
		},
	}

	got := preflightPlanEdits(root, plan)
	if got == nil || got.Reason != "edit has invalid path" {
		t.Fatalf("expected invalid path preflight mismatch, got %#v", got)
	}
}

func TestPreflightPlanEditsRejectsAmbiguousOldTextBeforeWrites(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "x.txt"), []byte("set_floor(30.0f);\nother();\nset_floor(30.0f);\n"), 0o644); err != nil {
		t.Fatalf("seed x.txt: %v", err)
	}
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{Path: "x.txt", OldText: "set_floor(30.0f);", NewText: "floor = 30.0f;"},
		},
	}
	got := preflightPlanEdits(root, plan)
	if got == nil {
		t.Fatalf("preflight must catch ambiguous old_text before executor dispatch")
	}
	if got.Index != 0 || got.Path != "x.txt" {
		t.Fatalf("mismatch should point at edit 0 in x.txt, got %#v", got)
	}
	if !strings.Contains(got.Reason, "matched 2 locations") {
		t.Fatalf("mismatch reason should report duplicate matches, got %q", got.Reason)
	}
}

func TestPreflightPlanEditsAllowsReplaceAllRepeatedOldText(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "x.txt"), []byte("set_floor(30.0f);\nset_floor(30.0f);\n"), 0o644); err != nil {
		t.Fatalf("seed x.txt: %v", err)
	}
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{Path: "x.txt", OldText: "set_floor(30.0f);", NewText: "floor = 30.0f;", ReplaceAll: true},
		},
	}
	if got := preflightPlanEdits(root, plan); got != nil {
		t.Fatalf("replace_all repeated old_text should pass preflight, got %#v", got)
	}
}

func TestPreflightPlanUnsupportedProofClaimsRejectsNoOpAssertion(t *testing.T) {
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{
				Path:    "tests/panel_contract.gd",
				NewText: "func test_contract():\n\t_assert_true(true, \"contract validated\")\n",
			},
		},
	}
	got := preflightPlanUnsupportedProofClaims(plan)
	if got == nil {
		t.Fatalf("expected unsupported proof preflight to reject no-op assertion")
	}
	if got.Path != "tests/panel_contract.gd" || !strings.Contains(got.Reason, "no-op assertion") {
		t.Fatalf("unexpected mismatch: %#v", got)
	}
}

func TestPreflightPlanUnsupportedProofClaimsRejectsSelfDisqualifyingProofComment(t *testing.T) {
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{
				Path:    "tests/panel_contract.gd",
				NewText: "func test_contract():\n\t# simplified check: for now, we just verify that it exists and can be called\n\tpass\n",
			},
		},
	}
	got := preflightPlanUnsupportedProofClaims(plan)
	if got == nil {
		t.Fatalf("expected unsupported proof preflight to reject self-disqualifying proof comment")
	}
	if got.Path != "tests/panel_contract.gd" || !strings.Contains(got.Reason, "self-disqualifying") {
		t.Fatalf("unexpected mismatch: %#v", got)
	}
}

func TestPreflightPlanUnsupportedProofClaimsIgnoresProseFiles(t *testing.T) {
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{
				Path:    "docs/review.md",
				NewText: "This retrospective says we cannot easily test the old behavior.\n",
			},
		},
	}
	if got := preflightPlanUnsupportedProofClaims(plan); got != nil {
		t.Fatalf("prose files should not trigger source proof preflight, got %#v", got)
	}
}

func TestPreflightPlanEditsSkipsRangeShapedEdits(t *testing.T) {
	// Range-shaped edits (start_line+end_line+content, no old_text)
	// don't depend on substring matching; preflight must skip them
	// and let the executor handle their validation.
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "x.txt"), []byte("a\nb\nc\n"), 0o644); err != nil {
		t.Fatalf("seed x.txt: %v", err)
	}
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{Path: "x.txt", StartLine: 1, EndLine: 1, Content: "A\n"},
		},
	}
	if got := preflightPlanEdits(root, plan); got != nil {
		t.Fatalf("preflight must skip range-shaped edits, got mismatch %#v", got)
	}
}

func TestPreflightPlanEditsAcceptsInsertAfterLineShape(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "x.txt"), []byte("a\nb\n"), 0o644); err != nil {
		t.Fatalf("seed x.txt: %v", err)
	}
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{Path: "x.txt", InsertAfterLine: 1, Content: "inserted"},
		},
	}
	if got := preflightPlanEdits(root, plan); got != nil {
		t.Fatalf("preflight must accept insert_after_line edits, got mismatch %#v", got)
	}
}

func TestRunExecutorPhaseAppliesInsertAfterLineShape(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "x.txt"), []byte("a\nb"), 0o644); err != nil {
		t.Fatalf("seed x.txt: %v", err)
	}
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{Path: "x.txt", InsertAfterLine: 2, Content: "c\n", Purpose: "Append c."},
		},
	}
	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}
	var stdout bytes.Buffer
	report, err := runExecutorPhase(cfg, plan, []string{"x.txt"}, true, &stdout)
	if err != nil {
		t.Fatalf("executor phase failed: %v\n%s", err, stdout.String())
	}
	if report.Succeeded != 1 || report.Failed != 0 {
		t.Fatalf("unexpected report: %#v", report)
	}
	got, err := os.ReadFile(filepath.Join(root, "x.txt"))
	if err != nil {
		t.Fatalf("read x.txt: %v", err)
	}
	if string(got) != "a\nb\nc\n" {
		t.Fatalf("insert_after_line executor result mismatch: %q", string(got))
	}
}

func TestPreflightPlanEditsRejectsAdjacentDuplicateCodeLine(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "sim.cpp"), []byte("void tick() {\n    auto& c = entities_->colonists.data()[i];\n    c.cultural_work_mult = 1.0f;\n}\n"), 0o644); err != nil {
		t.Fatalf("seed sim.cpp: %v", err)
	}
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{
				Path:    "sim.cpp",
				OldText: "    c.cultural_work_mult = 1.0f;\n",
				NewText: "    auto& c = entities_->colonists.data()[i];\n    c.cultural_work_mult = 1.0f;\n",
			},
		},
	}
	got := preflightPlanEdits(root, plan)
	if got == nil {
		t.Fatalf("preflight must reject plans that introduce adjacent duplicate code")
	}
	if got.Index != 0 || got.Path != "sim.cpp" {
		t.Fatalf("mismatch should point at edit 0 in sim.cpp, got %#v", got)
	}
	if !strings.Contains(got.Reason, "adjacent duplicate code line") {
		t.Fatalf("mismatch reason should name adjacent duplicate code, got %q", got.Reason)
	}
}

func TestRunExecutorPhaseRejectsCompletedArtifactOverwriteBeforeWrites(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("before\n"), 0o644); err != nil {
		t.Fatalf("seed a.txt: %v", err)
	}
	specPath := filepath.Join(root, ".planning", "atoms", "done.md")
	if err := os.MkdirAll(filepath.Dir(specPath), 0o755); err != nil {
		t.Fatalf("mkdir spec parent: %v", err)
	}
	originalSpec := "# Done\n\nStatus: complete\n\nAccepted spec.\n"
	if err := os.WriteFile(specPath, []byte(originalSpec), 0o644); err != nil {
		t.Fatalf("seed spec: %v", err)
	}
	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{Path: "a.txt", OldText: "before", NewText: "after"},
			{Path: ".planning/atoms/done.md", NewText: "# Done\n\nNew draft.\n", ReplaceAll: true},
		},
	}
	var stdout bytes.Buffer
	report, err := runExecutorPhase(cfg, plan, nil, false, &stdout)
	if err == nil {
		t.Fatalf("expected completed artifact overwrite preflight failure")
	}
	if report.Succeeded != 0 || report.Failed != 0 {
		t.Fatalf("preflight failure should happen before dispatch, got %#v", report)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "a.txt")); string(got) != "before\n" {
		t.Fatalf("preflight abort must leave earlier files untouched, got %q", string(got))
	}
	if got, _ := os.ReadFile(specPath); string(got) != originalSpec {
		t.Fatalf("preflight abort must leave completed spec untouched, got %q", string(got))
	}
}

func TestRunExecutorPhaseRejectsEmptyPlan(t *testing.T) {
	cfg := config.Config{Workspace: config.Workspace{Root: t.TempDir()}}
	var stdout bytes.Buffer
	_, err := runExecutorPhase(cfg, workspace.PlanArtifact{SchemaVersion: workspace.PlanArtifactSchemaVersion}, nil, false, &stdout)
	if err == nil {
		t.Fatalf("empty plan should be rejected")
	}
}

func TestFindLatestPlanArtifactReturnsMostRecent(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".idiom", "plans")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Write two stamped plans + the rolling pointer.
	old := filepath.Join(dir, "20260101-000000000.plan.json")
	newer := filepath.Join(dir, "20260101-000001000.plan.json")
	pointer := filepath.Join(dir, "last.plan.json")
	for _, p := range []string{old, newer, pointer} {
		if err := os.WriteFile(p, []byte("{}"), 0o644); err != nil {
			t.Fatalf("seed %s: %v", p, err)
		}
	}
	// Force the newer one to have a later mtime regardless of write
	// order.
	if err := os.Chtimes(newer, mustTime("2026-04-01T00:00:00Z"), mustTime("2026-04-01T00:00:00Z")); err != nil {
		t.Fatalf("chtimes newer: %v", err)
	}
	if err := os.Chtimes(old, mustTime("2026-01-01T00:00:00Z"), mustTime("2026-01-01T00:00:00Z")); err != nil {
		t.Fatalf("chtimes old: %v", err)
	}
	got, err := findLatestPlanArtifact(root, time.Time{}, "")
	if err != nil {
		t.Fatalf("findLatestPlanArtifact: %v", err)
	}
	if filepath.Base(got) != filepath.Base(newer) {
		t.Fatalf("expected newer artifact, got %s", got)
	}
}

func TestFindLatestPlanArtifactErrorsWhenNoneExist(t *testing.T) {
	root := t.TempDir()
	if _, err := findLatestPlanArtifact(root, time.Time{}, ""); err == nil {
		t.Fatalf("expected error when plans dir missing")
	}
}

func TestFindLatestPlanArtifactFiltersBySessionID(t *testing.T) {
	// The headline concurrency-isolation invariant: when a session ID
	// is supplied, plans from other sessions (even newer ones) must be
	// invisible. Without this filter, two concurrent plan-execute runs
	// in the same workspace can load each other's plans and dispatch
	// the wrong edits.
	root := t.TempDir()
	dir := filepath.Join(root, ".idiom", "plans")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	mine := filepath.Join(dir, "20260510-180000000.plan.json")
	otherNewer := filepath.Join(dir, "20260510-180100000.plan.json")
	if err := os.WriteFile(mine, []byte(`{"schema_version":"idiom.plan.v1","session_id":"sid-mine","edits":[{"path":"a","old_text":"x","new_text":"y"}]}`), 0o644); err != nil {
		t.Fatalf("seed mine: %v", err)
	}
	if err := os.WriteFile(otherNewer, []byte(`{"schema_version":"idiom.plan.v1","session_id":"sid-other","edits":[{"path":"a","old_text":"x","new_text":"y"}]}`), 0o644); err != nil {
		t.Fatalf("seed other: %v", err)
	}
	if err := os.Chtimes(mine, mustTime("2026-05-10T18:00:00Z"), mustTime("2026-05-10T18:00:00Z")); err != nil {
		t.Fatalf("chtimes mine: %v", err)
	}
	if err := os.Chtimes(otherNewer, mustTime("2026-05-10T18:01:00Z"), mustTime("2026-05-10T18:01:00Z")); err != nil {
		t.Fatalf("chtimes other: %v", err)
	}
	// With sessionID filter: mine is selected even though other is newer.
	got, err := findLatestPlanArtifact(root, time.Time{}, "sid-mine")
	if err != nil {
		t.Fatalf("findLatestPlanArtifact: %v", err)
	}
	if filepath.Base(got) != filepath.Base(mine) {
		t.Fatalf("expected my session's plan even though other is newer, got %s", got)
	}
	// Without sessionID (legacy callers): newest wins regardless of attribution.
	got, err = findLatestPlanArtifact(root, time.Time{}, "")
	if err != nil {
		t.Fatalf("findLatestPlanArtifact (no session): %v", err)
	}
	if filepath.Base(got) != filepath.Base(otherNewer) {
		t.Fatalf("legacy lookup should return newest, got %s", got)
	}
	// Filter for an unknown session: error, even though other plans exist.
	if _, err := findLatestPlanArtifact(root, time.Time{}, "sid-nobody"); err == nil {
		t.Fatalf("expected error when no plan matches the requested session")
	}
}

func TestFindLatestPlanArtifactSkipsArtifactsBeforeNotBefore(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".idiom", "plans")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	stale := filepath.Join(dir, "20260101-000000000.plan.json")
	if err := os.WriteFile(stale, []byte("{}"), 0o644); err != nil {
		t.Fatalf("seed stale: %v", err)
	}
	if err := os.Chtimes(stale, mustTime("2026-01-01T00:00:00Z"), mustTime("2026-01-01T00:00:00Z")); err != nil {
		t.Fatalf("chtimes stale: %v", err)
	}
	notBefore := mustTime("2026-04-01T00:00:00Z")
	if _, err := findLatestPlanArtifact(root, notBefore, ""); err == nil {
		t.Fatalf("expected error when only stale artifacts exist before notBefore")
	}
}
