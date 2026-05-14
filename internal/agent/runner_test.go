package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"idiom/internal/config"
	"idiom/internal/frontier/workerruntime"
	"idiom/internal/llama"
	"idiom/internal/workspace"
)

func TestVerificationContractSkipsGate(t *testing.T) {
	cases := []struct {
		contract string
		want     bool
	}{
		{"", false},
		{"tests_passed_or_failed_or_skipped_with_blocker", false},
		{"not_required_for_text_mutations", true},
		{"not_required_for_read_only_lane", true},
		{"not_required_for_anything_at_all", true},
		{"  not_required_for_x  ", true}, // whitespace tolerated
	}
	for _, tc := range cases {
		if got := verificationContractSkipsGate(tc.contract); got != tc.want {
			t.Errorf("verificationContractSkipsGate(%q) = %v, want %v", tc.contract, got, tc.want)
		}
	}
}

func TestLanguageDiagnosticsCountAsVerification(t *testing.T) {
	diagnosticsTools := []string{
		"go_diagnostics",
		"cpp_diagnostics",
		"python_diagnostics",
		"ts_diagnostics",
		"rust_diagnostics",
		"java_diagnostics",
	}
	for _, tool := range diagnosticsTools {
		req := workspace.ToolRequest{Tool: tool}
		if !isVerificationRequest(req) {
			t.Errorf("%s should count as a verification request", tool)
		}
		if !hasSuccessfulVerificationResult([]workspace.ToolResult{{Tool: tool, Summary: "Collected diagnostics"}}) {
			t.Errorf("%s should count as successful verification when it has no error", tool)
		}
		if hasSuccessfulVerificationResult([]workspace.ToolResult{{Tool: tool, Summary: "Diagnostics failed", Error: "failed"}}) {
			t.Errorf("%s with an error should not count as successful verification", tool)
		}
	}
}

func TestShellSanityCommandsCountAsVerification(t *testing.T) {
	req := workspace.ToolRequest{Tool: "shell", Command: "git diff --check"}
	if !isVerificationRequest(req) {
		t.Fatal("git diff --check should count as a verification request after writes")
	}
	if !hasSuccessfulVerificationResult([]workspace.ToolResult{{Tool: "shell", Summary: "Ran shell command", Output: "$ git diff --check\n"}}) {
		t.Fatal("git diff --check shell result should count as successful verification")
	}

	state := newRunnerExecutionState(false, 0)
	state.writesSinceVerification = 2
	if state.shouldRequireVerification(req, workerruntime.WorkerRuntimeBrief{VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker"}) {
		t.Fatal("post-write verification gate should allow git diff --check")
	}

	if !isVerificationRequest(workspace.ToolRequest{Tool: "source_sanity", Path: "native/tests/test_cart_system.cpp"}) {
		t.Fatal("source_sanity should count as a verification request")
	}
	if !hasSuccessfulVerificationResult([]workspace.ToolResult{{Tool: "source_sanity", Summary: "Source sanity passed"}}) {
		t.Fatal("successful source_sanity should count as verification")
	}
}

func TestPatchHygieneDoesNotSatisfySourceEditCompletion(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:           "implementation",
			Contract:             "implementation_result",
			VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
		},
	}
	results := []workspace.ToolResult{
		{Tool: "edit_file", Summary: "Edited native/tests/test_cart_system.cpp"},
		{Tool: "shell", Summary: "Ran shell command", Output: "$ git diff --check -- native/tests/test_cart_system.cpp\n"},
	}

	if result, rejected := runner.mutationCompletionRejection("Changed paths:\n- native/tests/test_cart_system.cpp\n\nVerification: git diff --check passed.", results); !rejected || !strings.Contains(result.Error, "patch hygiene") {
		t.Fatalf("expected source edit completion to reject patch-only verification, rejected=%v result=%#v", rejected, result)
	}

	results = append(results, workspace.ToolResult{Tool: "cpp_diagnostics", Summary: "Collected C++ diagnostics for 1 file(s).", Output: "native/tests/test_cart_system.cpp: no warnings or errors"})
	if result, rejected := runner.mutationCompletionRejection("Changed paths:\n- native/tests/test_cart_system.cpp\n\nVerification: cpp_diagnostics passed.", results); rejected {
		t.Fatalf("expected language diagnostics to satisfy source edit completion, got %#v", result)
	}
}

func TestPlannerOwnedVerificationRequiresSourceSanityBeforeDelegatedStatus(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:           "implementation",
			Contract:             "implementation_result",
			VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
			Constraints:          []string{"planner_owned_verification"},
		},
	}
	results := []workspace.ToolResult{
		{Tool: "edit_file", Summary: "Edited native/tests/test_cart_system.cpp"},
		{Tool: "shell", Summary: "Ran shell command", Output: "$ git diff --check -- native/tests/test_cart_system.cpp\n"},
	}

	patchOnlyAnswer := "Changed paths:\n- native/tests/test_cart_system.cpp\n\nVerification: git diff --check passed."
	if result, rejected := runner.mutationCompletionRejection(patchOnlyAnswer, results); !rejected || !strings.Contains(result.Error, "planner-owned") {
		t.Fatalf("expected planner-owned source edit closeout to require delegated verification status, rejected=%v result=%#v", rejected, result)
	}

	delegatedAnswer := patchOnlyAnswer + "\nPlanner-owned validation not run by lane; build/test verification is planner-owned."
	if result, rejected := runner.mutationCompletionRejection(delegatedAnswer, results); !rejected || !strings.Contains(result.Error, "source-sanity") {
		t.Fatalf("expected planner-owned delegated status without source sanity to reject, rejected=%v result=%#v", rejected, result)
	}

	results = append(results, workspace.ToolResult{Tool: "source_sanity", Summary: "Source sanity passed", Output: "native/tests/test_cart_system.cpp: generic delimiter sanity passed"})
	if result, rejected := runner.mutationCompletionRejection(delegatedAnswer+"\nSource sanity: passed.", results); rejected {
		t.Fatalf("expected source_sanity plus delegated build/test status to satisfy closeout, got %#v", result)
	}
}

func TestClaimedChangedPathsStopBeforeVerificationStatusProse(t *testing.T) {
	answer := "Changed paths:\n- native/tests/test_cart_system.cpp\nPlanner-owned validation not run by lane; build/test verification is planner-owned."
	if got := claimedChangedPathsFromAnswer(answer); !reflect.DeepEqual(got, []string{"native/tests/test_cart_system.cpp"}) {
		t.Fatalf("expected verification prose outside changed paths, got %#v", got)
	}

	answer = "Changed paths:\n- docs/planner-owned.md\n\nVerification: git diff --check passed."
	if got := claimedChangedPathsFromAnswer(answer); !reflect.DeepEqual(got, []string{"docs/planner-owned.md"}) {
		t.Fatalf("expected planner-owned path to remain valid, got %#v", got)
	}
}

func TestUnsupportedClaimedChangedPathsIgnoresDescriptiveSlashPhrases(t *testing.T) {
	results := []workspace.ToolResult{
		{Tool: "apply_patch", Output: "update native/src/simulation/logistics/track_system.cpp\nupdate native/tests/test_transport_integration.cpp\n"},
	}
	answer := strings.Join([]string{
		"Changed paths:",
		"- native/src/simulation/logistics/track_system.cpp",
		"- test_transport_integration.cpp",
		"",
		"Verification: source/dest and capacity/latency cases covered; git diff --check passed.",
	}, "\n")

	if got := unsupportedClaimedChangedPaths(answer, results); len(got) != 0 {
		t.Fatalf("descriptive slash phrases and unique basenames should not be rejected as changed paths, got %#v", got)
	}

	answer = "Changed paths:\n- native/src/simulation/logistics/track_system.cpp\n- native/tests/missing_test.cpp\n"
	if got := unsupportedClaimedChangedPaths(answer, results); !reflect.DeepEqual(got, []string{"native/tests/missing_test.cpp"}) {
		t.Fatalf("real unbacked workspace path should still be rejected, got %#v", got)
	}
}

func TestMutationCompletionGateRequiresRequiredEditSnippets(t *testing.T) {
	root := t.TempDir()
	target := "native/tests/test_production_graph.cpp"
	if err := os.MkdirAll(filepath.Join(root, filepath.Dir(target)), 0o755); err != nil {
		t.Fatalf("mkdir target dir: %v", err)
	}
	includeSnippet := `#include "core/constants/logistics.h"`
	testSnippet := `TEST_CASE("demand pressure caps production") {
	CHECK(DEMAND_PRESSURE_CAP > 0.0);
}`
	if err := os.WriteFile(filepath.Join(root, target), []byte(includeSnippet+"\n"), 0o644); err != nil {
		t.Fatalf("write partial target: %v", err)
	}
	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:           "implementation",
			Contract:             "implementation_result",
			VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
			RequiredEditPaths:    []string{target},
			RequiredEditSnippets: []workerruntime.RequiredEditSnippet{
				{ID: "required_edit_1", Path: target, Content: includeSnippet},
				{ID: "required_edit_2", Path: target, Content: testSnippet},
			},
		},
	}
	results := []workspace.ToolResult{
		{Tool: "edit_file", Summary: "Edited " + target},
		{Tool: "shell", Summary: "Ran shell command", Output: "$ git diff --check -- " + target + "\n"},
	}

	result, rejected := runner.mutationCompletionRejection("Changed paths:\n- "+target+"\n\nVerification: git diff --check passed.", results)
	if !rejected || !strings.Contains(result.Error, "required_edit_2") {
		t.Fatalf("expected missing required snippet rejection, rejected=%v result=%#v", rejected, result)
	}
	if strings.Contains(result.Error, "patch hygiene") {
		t.Fatalf("required snippet coverage should fail before semantic verification, got %q", result.Error)
	}

	fullContent := includeSnippet + "\n\n" + testSnippet + "\n"
	if err := os.WriteFile(filepath.Join(root, target), []byte(fullContent), 0o644); err != nil {
		t.Fatalf("write complete target: %v", err)
	}
	verifiedResults := []workspace.ToolResult{
		{Tool: "edit_file", Summary: "Edited " + target},
		{Tool: "cpp_diagnostics", Summary: "Collected C++ diagnostics for 1 file(s).", Output: target + ": no warnings or errors"},
	}
	if result, rejected := runner.mutationCompletionRejection("Changed paths:\n- "+target+"\n\nVerification: cpp_diagnostics passed.", verifiedResults); rejected {
		t.Fatalf("expected complete snippets with diagnostics to pass, got %#v", result)
	}
}

func TestMutationCompletionGateRejectsSilentRemovedTestDeclaration(t *testing.T) {
	root := t.TempDir()
	target := "native/tests/test_transport_system.cpp"
	initial := `#include <catch2/catch_test_macros.hpp>

TEST_CASE("loader reserves assigned carts") {
	CHECK(true);
}

TEST_CASE("loader job dedupes and reposts while cart still needs loading") {
	CHECK(true);
}
`
	modified := `#include <catch2/catch_test_macros.hpp>

TEST_CASE("loader reserves assigned carts") {
	CHECK(true);
}
`
	writeRunnerTestFile(t, root, target, initial)
	initRunnerTestGitRepo(t, root)
	writeRunnerTestFile(t, root, target, modified)

	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:           "implementation",
			Contract:             "implementation_result",
			VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
			RequiredEditPaths:    []string{target},
		},
	}
	results := []workspace.ToolResult{
		{Tool: "edit_file", Summary: "Edited " + target},
		{Tool: "cpp_diagnostics", Summary: "Collected C++ diagnostics for 1 file(s).", Output: target + ": no warnings or errors"},
	}

	answer := "Changed paths:\n- " + target + "\n\nVerification: cpp_diagnostics passed."
	result, rejected := runner.mutationCompletionRejection(answer, results)
	if !rejected || !strings.Contains(result.Summary, "silent test removal") || !strings.Contains(result.Error, "loader job dedupes") {
		t.Fatalf("expected silent removed test declaration rejection, rejected=%v result=%#v", rejected, result)
	}
}

func TestMutationCompletionGateAllowsReportedRemovedTestDeclaration(t *testing.T) {
	root := t.TempDir()
	target := "native/tests/test_transport_system.cpp"
	initial := `#include <catch2/catch_test_macros.hpp>

TEST_CASE("loader reserves assigned carts") {
	CHECK(true);
}

TEST_CASE("loader job dedupes and reposts while cart still needs loading") {
	CHECK(true);
}
`
	modified := `#include <catch2/catch_test_macros.hpp>

TEST_CASE("loader reserves assigned carts") {
	CHECK(true);
}
`
	writeRunnerTestFile(t, root, target, initial)
	initRunnerTestGitRepo(t, root)
	writeRunnerTestFile(t, root, target, modified)

	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:           "implementation",
			Contract:             "implementation_result",
			VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
			RequiredEditPaths:    []string{target},
		},
	}
	results := []workspace.ToolResult{
		{Tool: "edit_file", Summary: "Edited " + target},
		{Tool: "cpp_diagnostics", Summary: "Collected C++ diagnostics for 1 file(s).", Output: target + ": no warnings or errors"},
	}

	answer := strings.Join([]string{
		"Changed paths:",
		"- " + target,
		"",
		"Removed tests:",
		"- loader job dedupes and reposts while cart still needs loading",
		"",
		"Verification: cpp_diagnostics passed.",
	}, "\n")
	if result, rejected := runner.mutationCompletionRejection(answer, results); rejected {
		t.Fatalf("expected reported test removal to pass mutation completion, got %#v", result)
	}
}

func writeRunnerTestFile(t *testing.T, root string, path string, content string) {
	t.Helper()
	abs := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(abs), err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", abs, err)
	}
}

func initRunnerTestGitRepo(t *testing.T, root string) {
	t.Helper()
	ctx := context.Background()
	if _, err := gitCombinedOutput(ctx, root, "init"); err != nil {
		t.Skipf("git unavailable for diff-backed completion gate test: %v", err)
	}
	for _, args := range [][]string{
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test User"},
		{"add", "."},
		{"commit", "-m", "initial"},
	} {
		if _, err := gitCombinedOutput(ctx, root, args...); err != nil {
			t.Fatalf("git %s: %v", strings.Join(args, " "), err)
		}
	}
}

func TestPatchHygieneAllowsNonSourceCompletion(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:           "implementation",
			Contract:             "implementation_result",
			VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
		},
	}
	results := []workspace.ToolResult{
		{Tool: "edit_file", Summary: "Edited README.md"},
		{Tool: "shell", Summary: "Ran shell command", Output: "$ git diff --check -- README.md\n"},
	}

	if result, rejected := runner.mutationCompletionRejection("Changed paths:\n- README.md\n\nVerification: git diff --check passed.", results); rejected {
		t.Fatalf("expected patch hygiene to satisfy non-source edit completion, got %#v", result)
	}
}

func TestSourceEditPatchHygieneBlocksMoreReadsButAllowsSourceSanity(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:           "implementation",
		Contract:             "implementation_result",
		VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
	}
	results := []workspace.ToolResult{
		{Tool: "edit_file", Summary: "Edited native/src/cart_system.cpp"},
		{Tool: "shell", Summary: "Ran shell command", Output: "$ git diff --check -- native/src/cart_system.cpp\n"},
	}

	summary, errorText, reject := sourceEditVerificationRejection(workspace.ToolRequest{Tool: "read_file", Path: "native/src/cart_system.cpp"}, brief, results)
	if !reject || summary != "Source edit verification required" || !strings.Contains(errorText, "source-sanity") {
		t.Fatalf("expected patch-only source verification to block more reads with source-sanity guidance, got reject=%v summary=%q error=%q", reject, summary, errorText)
	}
	if strings.Contains(strings.ToLower(errorText), "run source_sanity") || strings.Contains(strings.ToLower(errorText), "use source_sanity") {
		t.Fatalf("source verification recovery should name evidence, not prescribe the next tool: %q", errorText)
	}
	if _, _, reject := sourceEditVerificationRejection(workspace.ToolRequest{Tool: "source_sanity", Path: "native/src/cart_system.cpp"}, brief, results); reject {
		t.Fatal("source_sanity must remain allowed after patch-only source verification")
	}
}

func TestSourceEditCloseoutAfterSemanticVerificationRequiresNamedGapForReads(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:           "implementation",
		Contract:             "implementation_result",
		VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
	}
	results := []workspace.ToolResult{
		{Tool: "edit_file", Summary: "Edited native/src/cart_system.cpp"},
		{Tool: "cpp_diagnostics", Summary: "Collected C++ diagnostics for 1 file(s).", Output: "native/src/cart_system.cpp: no warnings or errors"},
	}

	prompt, ok := sourceEditCloseoutPrompt(workspace.ToolRequest{Tool: "read_file", Path: "native/src/cart_system.cpp"}, brief, results)
	if !ok || !strings.Contains(prompt, "source-aware verification") {
		t.Fatalf("expected verified source edit read to force closeout, ok=%v prompt=%q", ok, prompt)
	}
	if _, ok := sourceEditCloseoutPrompt(workspace.ToolRequest{Tool: "read_file", Path: "native/src/cart_system.cpp", NamedGap: "inspect function Foo branch after diagnostics"}, brief, results); ok {
		t.Fatal("read with a named gap should remain model-directed")
	}
}

func TestSuccessfulVerificationResultIgnoresNonVerificationShell(t *testing.T) {
	results := []workspace.ToolResult{{
		Tool:    "shell",
		Summary: "Ran shell command",
		Output:  "$ cd\n",
	}}
	if hasSuccessfulVerificationResult(results) {
		t.Fatal("non-verification shell command must not satisfy mutation verification gate")
	}
}

func TestEscalateCompletionGateMessageTiers(t *testing.T) {
	base := "workspace changes are present, but no successful verification result is present."

	// Tier 0 (first rejection): pass through unchanged.
	if got := escalateCompletionGateMessage(base, 0); got != base {
		t.Errorf("first rejection should pass through; got: %s", got)
	}

	// Tier 1 (second rejection): mention "[gate retry 2/3]" + the base.
	got := escalateCompletionGateMessage(base, 1)
	for _, want := range []string{"[gate retry 2/3]", "no successful verification result", "MUST resolve"} {
		if !strings.Contains(got, want) {
			t.Errorf("tier 1 expected to contain %q, got: %s", want, got)
		}
	}

	// Tier 2 (third rejection): mention "[gate retry 3/3]" + escalation language.
	got = escalateCompletionGateMessage(base, 2)
	for _, want := range []string{"[gate retry 3/3", "tools disable", "ONE concrete tool call", "Do not narrate"} {
		if !strings.Contains(got, want) {
			t.Errorf("tier 2 expected to contain %q, got: %s", want, got)
		}
	}

	// Empty base: no escalation (defensive).
	if got := escalateCompletionGateMessage("", 5); got != "" {
		t.Errorf("empty base should pass through unchanged, got: %q", got)
	}
}

func TestToolActionClassNormalizesRepeatedDeadEndKeys(t *testing.T) {
	cases := []struct {
		req  workspace.ToolRequest
		want string
	}{
		{req: workspace.ToolRequest{Tool: "list_files"}, want: "broad_inspection"},
		{req: workspace.ToolRequest{Tool: "list_files", Path: "internal"}, want: "list_files:internal"},
		{req: workspace.ToolRequest{Tool: "shell", Command: "find . -maxdepth 2"}, want: "broad_inspection"},
		{req: workspace.ToolRequest{Tool: "shell", Command: "rg route internal/app"}, want: "shell_narrow:rg route internal/app"},
		{req: workspace.ToolRequest{Tool: "shell", Command: "git diff --check -- README.md"}, want: "shell_verification:git diff --check -- readme.md"},
		{req: workspace.ToolRequest{Tool: "read_file", ID: "f12"}, want: "read_file:f12"},
		{req: workspace.ToolRequest{Tool: "discovery_query", Purpose: "impact_analysis", Path: "internal/workspace/manager.go", Pattern: "Execute", Language: "go"}, want: "discovery_query:impact_analysis:internal/workspace/manager.go:execute:go"},
		{req: workspace.ToolRequest{Tool: "structural_search", Path: "internal", Pattern: "if $COND { $BODY }", Language: "go"}, want: "structural_search:internal:if $cond { $body }:go"},
		{req: workspace.ToolRequest{Tool: "dependency_graph", Path: ".", Language: "go"}, want: "dependency_graph:.:go"},
		{req: workspace.ToolRequest{Tool: "history_range", Path: "internal/workspace/manager.go", StartLine: 10, EndLine: 40}, want: "history_range:internal/workspace/manager.go:10-40"},
		{req: workspace.ToolRequest{Tool: "history_symbol", Path: "internal/workspace/manager.go", Pattern: "Execute"}, want: "history_symbol:internal/workspace/manager.go:execute"},
		{req: workspace.ToolRequest{Tool: "blame_range", Path: "internal/workspace/manager.go", StartLine: 1, EndLine: 2}, want: "blame_range:internal/workspace/manager.go:1-2"},
		{req: workspace.ToolRequest{Tool: "ctags_symbols", Path: ".", Pattern: "Manager"}, want: "ctags_symbols:.:manager"},
		{req: workspace.ToolRequest{Tool: "semgrep_scan", Path: ".", Pattern: "p/default"}, want: "semgrep_scan:.:p/default"},
		{req: workspace.ToolRequest{Tool: "joern_scan", Path: "src"}, want: "joern_scan:src"},
		{req: workspace.ToolRequest{Tool: "go_find_symbol", Pattern: "classifyPromptExecutionPlan"}, want: "go_find_symbol:classifypromptexecutionplan"},
		{req: workspace.ToolRequest{Tool: "go_file_outline", Path: "internal/app/app.go"}, want: "go_file_outline:internal/app/app.go"},
		{req: workspace.ToolRequest{Tool: "go_move_declarations", Path: "internal/app/app.go", Destination: "internal/app/rendering.go"}, want: "go_move_declarations:internal/app/app.go->internal/app/rendering.go"},
	}
	for _, tc := range cases {
		if got := toolActionClass(tc.req); got != tc.want {
			t.Fatalf("toolActionClass(%#v) = %q, want %q", tc.req, got, tc.want)
		}
	}
}

func TestVerificationShellActionClassDoesNotShareGenericGitDeadEnd(t *testing.T) {
	plainDiff := toolActionClass(workspace.ToolRequest{
		Tool:    "shell",
		Command: "git diff -- README.md",
	})
	verifyDiffCheck := toolActionClass(workspace.ToolRequest{
		Tool:    "shell",
		Command: "git diff --check -- README.md",
	})
	if plainDiff == verifyDiffCheck {
		t.Fatalf("verification shell action class must not match generic git action class: %q", plainDiff)
	}
	if !strings.HasPrefix(verifyDiffCheck, "shell_verification:") {
		t.Fatalf("verification command should use shell_verification action class, got %q", verifyDiffCheck)
	}
}

func TestPlannerOwnedVerificationRejectsBuildTestShell(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:  "implementation",
		Contract:    "implementation_result",
		Constraints: []string{"planner_owned_verification"},
	}
	for _, command := range []string{
		"cd native && scons -j1 -Q 2>&1 | head -20",
		"go test ./...",
		"cargo check",
		"npm run build",
		"python -m pytest",
		"python3 -m unittest",
		"python3 scripts/run_tests.py --build-only",
		"python3 tools/build.py",
		"make test",
	} {
		t.Run(command, func(t *testing.T) {
			summary, _, reject := plannerOwnedVerificationShellRejection(
				workspace.ToolRequest{Tool: "shell", Command: command},
				brief,
			)
			if !reject || summary != "Planner-owned verification shell rejected" {
				t.Fatalf("expected planner-owned build/test rejection, got reject=%t summary=%q", reject, summary)
			}
		})
	}
}

func TestPlannerOwnedVerificationAllowsCheapSanityShell(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:  "implementation",
		Contract:    "implementation_result",
		Constraints: []string{"planner_owned_verification"},
	}
	for _, command := range []string{
		"git diff --check -- README.md",
		"git status --short",
	} {
		t.Run(command, func(t *testing.T) {
			if summary, _, reject := plannerOwnedVerificationShellRejection(
				workspace.ToolRequest{Tool: "shell", Command: command},
				brief,
			); reject {
				t.Fatalf("unexpected planner-owned rejection %q for %q", summary, command)
			}
		})
	}
}

func TestRequiredEditPathScopeRejectsWritesOutsideAllowlist(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		RequiredEditPaths: []string{"native/src/job_queue.cpp", "scripts/ui/status_panel.gd"},
	}

	if summary, _, reject := requiredEditPathScopeRejection(
		workspace.ToolRequest{Tool: "edit_file", Path: "native/src/job_queue.cpp"},
		brief,
	); reject {
		t.Fatalf("allowed path should not be rejected: %q", summary)
	}

	summary, errorText, reject := requiredEditPathScopeRejection(
		workspace.ToolRequest{Tool: "write_file", Path: "native/src/other.cpp"},
		brief,
	)
	if !reject || summary != "Write outside required edit paths rejected" {
		t.Fatalf("expected out-of-scope write rejection, reject=%t summary=%q", reject, summary)
	}
	if !strings.Contains(errorText, "native/src/other.cpp") || !strings.Contains(errorText, "native/src/job_queue.cpp") {
		t.Fatalf("expected error to name attempted and allowed paths, got %q", errorText)
	}
}

func TestRequiredEditPathScopeAllowsParentMkdir(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		RequiredEditPaths: []string{"native/src/new_system/job_queue.cpp"},
	}

	if summary, _, reject := requiredEditPathScopeRejection(
		workspace.ToolRequest{Tool: "mkdir", Path: "native/src/new_system"},
		brief,
	); reject {
		t.Fatalf("parent mkdir for required edit path should be allowed, got %q", summary)
	}
}

func TestPlannerOwnedVerificationGateNamesLaneSanity(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:  "implementation",
		Contract:    "implementation_result",
		Constraints: []string{"planner_owned_verification"},
	}

	summary, errorText := verificationRequiredAfterWritesMessage(brief, 2)
	if summary != "Verification required after workspace edits" {
		t.Fatalf("unexpected summary %q", summary)
	}
	for _, want := range []string{"delegates build/test verification to the planner", "source-sanity evidence", "git diff --check", "do not run build or test"} {
		if !strings.Contains(errorText, want) {
			t.Fatalf("expected planner-owned verification message to contain %q, got %q", want, errorText)
		}
	}
	if strings.Contains(errorText, "go test ./...") {
		t.Fatalf("planner-owned verification message should not suggest full tests: %q", errorText)
	}
}

func TestHideBroadDiscoveryToolsIncludesHeavyGenericDiscovery(t *testing.T) {
	hidden := map[string]bool{}
	hideBroadDiscoveryTools(hidden)
	for _, want := range []string{"list_files", "discovery_query", "dependency_graph", "ctags_symbols", "semgrep_scan", "joern_scan", "go_overview"} {
		if !hidden[want] {
			t.Fatalf("expected broad/heavy discovery tool %q to be hidden after child packet, got %#v", want, hidden)
		}
	}
	for _, allowed := range []string{"structural_search", "history_range", "history_symbol", "blame_range"} {
		if hidden[allowed] {
			t.Fatalf("targeted discovery tool %q should remain available, got %#v", allowed, hidden)
		}
	}
}

func TestAdvertisedAllowedToolsPreservesRuntimeBriefOrder(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			AllowedTools: []string{"structural_search", "dependency_graph", "task", "list_files"},
		},
	}
	got := runner.advertisedAllowedTools([]string{"task", "list_files", "structural_search", "dependency_graph", "read_file"})
	want := []string{"structural_search", "dependency_graph", "task", "list_files"}
	if !slices.Equal(got, want) {
		t.Fatalf("advertisedAllowedTools order = %#v, want %#v", got, want)
	}
}

func TestTextOnlyChatOptionsUsesContractAwareCaps(t *testing.T) {
	tool := llama.Tool{Type: "function", Function: llama.ToolFunction{Name: "read_file"}}
	cases := []struct {
		name     string
		contract string
		input    int
		want     int
	}{
		{name: "direct answer", contract: "direct_answer", input: 8192, want: 1024},
		{name: "grounded explanation", contract: "grounded_explanation", input: 8192, want: 1536},
		{name: "delegated evidence summary", contract: "grounded_evidence_summary", input: 8192, want: 1536},
		{name: "architecture assessment", contract: "architecture_assessment", input: 8192, want: 2048},
		{name: "unknown contract", contract: "unknown", input: 8192, want: 768},
		{name: "preserves smaller explicit cap", contract: "architecture_assessment", input: 256, want: 256},
		{name: "fills unset cap", contract: "grounded_explanation", input: 0, want: 1536},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := textOnlyChatOptions(
				llama.ChatOptions{MaxTokens: tc.input, Tools: []llama.Tool{tool}, ToolChoice: "auto"},
				workerruntime.WorkerRuntimeBrief{Contract: tc.contract},
			)
			if got.MaxTokens != tc.want {
				t.Fatalf("MaxTokens = %d, want %d", got.MaxTokens, tc.want)
			}
			if len(got.Tools) != 0 || got.ToolChoice != "" {
				t.Fatalf("text-only options should disable tools, got tools=%#v choice=%q", got.Tools, got.ToolChoice)
			}
		})
	}
}

func TestPinnedPathsFromToolResultsRejectsToolJSONFragments(t *testing.T) {
	paths := pinnedPathsFromToolResults([]workspace.ToolResult{
		{
			Tool:    "go_move_declarations",
			Summary: `Blocked go_move_declarations with path:"internal/app/analysis_agents.go" and moves: {destination:"internal/app/orchestration.go", pattern:"tag:foo"}.`,
			Output:  `{"tool":"go_move_declarations","path":"internal/app/analysis_agents.go:12:3","moves":[{"destination":"internal/app/implementation.go"}]}`,
			Error:   "internal/app/analysis_agents.go:12:3: move was outside approval scope",
		},
	}, 8)

	if !containsRunnerString(paths, "internal/app/analysis_agents.go") {
		t.Fatalf("expected source path to be pinned, got %#v", paths)
	}
	for _, forbidden := range []string{"internal/app/orchestration.go", "internal/app/implementation.go"} {
		if containsRunnerString(paths, forbidden) {
			t.Fatalf("destination path %s must not be pinned as context, got %#v", forbidden, paths)
		}
	}
	for _, path := range paths {
		if strings.ContainsAny(path, "\"'\\{}[],:") {
			t.Fatalf("pinned path should be normalized, got %#v in %#v", path, paths)
		}
	}
}

func TestBuildRunnerNextActionStateGuidesAfterBootstrappedReadBlocker(t *testing.T) {
	req := workspace.ToolRequest{Tool: "read_file", Path: "internal/app/app.go"}
	result := workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Implementation bootstrap already inspected this target",
		Error:   "internal/app/app.go has already been read in the implementation bootstrap; use edit_file or a narrow line range instead",
		Output:  "internal/app/app.go: already inspected",
	}
	state := buildRunnerNextActionState(
		"implement",
		3,
		"pre_execute_guard",
		req,
		result,
		[]workspace.ToolResult{result},
		true,
		workerruntime.WorkerRuntimeBrief{},
	)
	if state.Lane != "implement" || state.Step != 3 || state.LastTool != "read_file" || state.LastTarget != "internal/app/app.go" {
		t.Fatalf("unexpected state identity: %#v", state)
	}
	if !strings.Contains(strings.Join(state.Constraints, "\n"), "declaration_moves_forbidden") {
		t.Fatalf("expected declaration move constraint, got %#v", state.Constraints)
	}
	if !strings.Contains(strings.Join(state.Constraints, "\n"), "bootstrap_evidence_available") {
		t.Fatalf("expected bootstrap evidence constraint, got %#v", state.Constraints)
	}
	if !strings.Contains(strings.Join(state.InvalidNextActions, "\n"), "duplicate_read_file_on_bootstrapped_target") {
		t.Fatalf("expected duplicate read invalid action, got %#v", state.InvalidNextActions)
	}
	actions := strings.Join(state.NextValidActions, "\n")
	if !strings.Contains(actions, "approved-target write capability") {
		t.Fatalf("expected edit guidance, got %#v", state.NextValidActions)
	}
	if !strings.Contains(actions, "narrow range read") {
		t.Fatalf("expected narrow read guidance, got %#v", state.NextValidActions)
	}
}

func TestRunnerEmitsNextActionStateAfterPreExecuteGuard(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "README.md", "hello\n"); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	replies := []string{
		"```tool\n{\"tool\":\"read_file\",\"path\":\"README.md\"}\n```",
		"Blocked by the read guard.",
	}
	type requestPayload struct {
		Messages []llama.ChatMessage `json:"messages"`
	}
	var payloads []requestPayload
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request")
		}
		var payload requestPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		payloads = append(payloads, payload)
		reply := replies[requests]
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))
	defer server.Close()

	var states []runnerNextActionState
	var feedback []runnerFailureFeedbackPacket
	var stepInstructions []string
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableShell:  true,
			EnableWrites: true,
		}),
		Policy: RunPolicy{MaxToolSteps: 2, MaxNoProgressTurns: 2},
		PreExecute: func(step int, req workspace.ToolRequest) *workspace.ToolResult {
			if req.Tool != "read_file" {
				return nil
			}
			return &workspace.ToolResult{
				Tool:    req.Tool,
				Summary: "Implementation bootstrap already inspected this target",
				Error:   "README.md has already been read in the implementation bootstrap; approved-target write capability or a narrow line range remains available",
			}
		},
		OnEvent: func(event RunEvent) {
			switch event.Role {
			case "next_action_state":
				var state runnerNextActionState
				if err := json.Unmarshal([]byte(event.Content), &state); err != nil {
					t.Fatalf("decode next action state %q: %v", event.Content, err)
				}
				states = append(states, state)
			case "failure_feedback":
				var packet runnerFailureFeedbackPacket
				if err := json.Unmarshal([]byte(event.Content), &packet); err != nil {
					t.Fatalf("decode failure feedback %q: %v", event.Content, err)
				}
				feedback = append(feedback, packet)
			case "step_instruction":
				stepInstructions = append(stepInstructions, event.Content)
			default:
				return
			}
		},
	}
	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "read README.md"}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if answer != "Blocked by the read guard." {
		t.Fatalf("unexpected answer %q", answer)
	}
	if len(states) != 1 {
		t.Fatalf("expected one next_action_state event, got %#v", states)
	}
	state := states[0]
	if state.Reason != "pre_execute_guard" || state.LastTool != "read_file" || state.LastTarget != "README.md" {
		t.Fatalf("unexpected state: %#v", state)
	}
	if !strings.Contains(strings.Join(state.NextValidActions, "\n"), "approved-target write capability") {
		t.Fatalf("expected edit guidance, got %#v", state.NextValidActions)
	}
	if len(feedback) != 1 {
		t.Fatalf("expected one failure feedback packet, got %#v", feedback)
	}
	if feedback[0].FailureClass != "guard_rejection" {
		t.Fatalf("expected frontier failure class, got %#v", feedback[0])
	}
	if !containsRunnerString(feedback[0].DoNotRepeat, "duplicate_read_file_on_bootstrapped_target") {
		t.Fatalf("expected duplicate read in do-not-repeat actions, got %#v", feedback[0])
	}
	if len(stepInstructions) != 0 {
		t.Fatalf("step_instruction events were removed — expected none, got %#v", stepInstructions)
	}
	if len(payloads) < 2 {
		t.Fatalf("expected second model request after guard, got %d", len(payloads))
	}
	var stepInstructionCount int
	for _, message := range payloads[1].Messages {
		if strings.Contains(message.Content, "[frontier step instruction]") {
			stepInstructionCount++
		}
		if strings.Contains(message.Content, "[failure feedback packet]") {
			t.Fatalf("second model request should not contain failure feedback packet: %q", message.Content)
		}
	}
	if stepInstructionCount > 1 {
		t.Fatalf("expected at most one persisted frontier step instruction, got %d", stepInstructionCount)
	}
}

func TestRunnerBootstrappedReadReminderDoesNotPoisonNarrowRead(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "README.md", "line 1\nline 2\n"); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	replies := []string{
		"```tool\n{\"tool\":\"read_file\",\"path\":\"README.md\"}\n```",
		"```tool\n{\"tool\":\"read_file\",\"path\":\"README.md\"}\n```",
		"```tool\n{\"tool\":\"read_file\",\"path\":\"README.md\",\"start_line\":1,\"end_line\":1}\n```",
		"Done after narrow read.",
	}
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request")
		}
		reply := replies[requests]
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))
	defer server.Close()

	var replayedBootstrapReminder bool
	var narrowReadSucceeded bool
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableShell:  true,
			EnableWrites: true,
		}),
		Policy: RunPolicy{MaxToolSteps: 5, MaxNoProgressTurns: 4},
		PreExecute: func(step int, req workspace.ToolRequest) *workspace.ToolResult {
			if req.Tool == "read_file" && req.Path == "README.md" && req.StartLine == 0 && req.EndLine == 0 {
				return &workspace.ToolResult{
					Tool:    req.Tool,
					Summary: "Implementation bootstrap already inspected this target",
					Error:   "README.md has already been read in the implementation bootstrap; use edit_file or a narrow line range instead",
				}
			}
			return nil
		},
	}
	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "use the bootstrapped README evidence"}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			if event.Result.Tool == "read_file" && strings.Contains(event.Result.Summary, "replayed same blocker") {
				replayedBootstrapReminder = true
			}
			if event.Result.Tool == "read_file" && event.Result.Error == "" && strings.Contains(event.Result.Summary, "lines 1-1") {
				narrowReadSucceeded = true
			}
			if strings.Contains(event.Result.Summary, "Repeated dead-end action rejected") {
				t.Fatalf("bootstrapped read reminder should not become a repeated dead-end: %#v", event.Result)
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if answer != "Done after narrow read." {
		t.Fatalf("unexpected answer %q", answer)
	}
	if !replayedBootstrapReminder {
		t.Fatal("expected repeated full read to replay the bootstrap reminder once")
	}
	if !narrowReadSucceeded {
		t.Fatal("expected narrow read to execute after bootstrapped-read reminder")
	}
}

func TestRunnerPreservesReasoningContentBetweenToolTurns(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "README.md", "hello\n"); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	type requestPayload struct {
		Messages []llama.ChatMessage `json:"messages"`
	}
	var payloads []requestPayload
	replies := []string{
		"{\"choices\":[{\"message\":{\"role\":\"assistant\",\"reasoning_content\":\"tool-call reasoning\",\"content\":\"```tool\\n{\\\"tool\\\":\\\"read_file\\\",\\\"path\\\":\\\"README.md\\\"}\\n```\"}}]}",
		`{"choices":[{"message":{"role":"assistant","content":"Done."}}]}`,
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload requestPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		payloads = append(payloads, payload)
		if len(payloads) > len(replies) {
			t.Fatalf("unexpected extra chat request")
		}
		_, _ = w.Write([]byte(replies[len(payloads)-1]))
	}))
	defer server.Close()

	enableThinking := true
	var thinkingEvents []runnerThinkingTrace
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 3, MaxNoProgressTurns: 2},
		OnEvent: func(event RunEvent) {
			if event.Role != "thinking_trace" {
				return
			}
			var trace runnerThinkingTrace
			if err := json.Unmarshal([]byte(event.Content), &trace); err != nil {
				t.Fatalf("decode thinking trace %q: %v", event.Content, err)
			}
			thinkingEvents = append(thinkingEvents, trace)
		},
	}
	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "read README.md"}},
		llama.ChatOptions{Model: "model", EnableThinking: &enableThinking, PreserveThinking: true, MaxTokens: 128},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if answer != "Done." {
		t.Fatalf("unexpected answer %q", answer)
	}
	if len(payloads) != 2 {
		t.Fatalf("expected two model requests, got %d", len(payloads))
	}
	var foundPreserved bool
	for _, message := range payloads[1].Messages {
		if message.Role == "assistant" && message.ReasoningContent == "tool-call reasoning" {
			foundPreserved = true
			break
		}
	}
	if !foundPreserved {
		t.Fatalf("expected second request to include prior assistant reasoning_content, got %#v", payloads[1].Messages)
	}
	var sawPreservedTrace bool
	for _, trace := range thinkingEvents {
		if trace.ReasoningChars > 0 && trace.PreservedInHistory {
			sawPreservedTrace = true
			break
		}
	}
	if !sawPreservedTrace {
		t.Fatalf("expected thinking_trace to report preserved reasoning, got %#v", thinkingEvents)
	}
}

func TestRunnerPreservesEmptyReplyReasoningForRetry(t *testing.T) {
	root := t.TempDir()
	type requestPayload struct {
		Messages []llama.ChatMessage `json:"messages"`
	}
	var payloads []requestPayload
	replies := []string{
		`{"choices":[{"message":{"role":"assistant","reasoning_content":"unfinished reasoning","content":""}}]}`,
		`{"choices":[{"message":{"role":"assistant","content":"Done."}}]}`,
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload requestPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		payloads = append(payloads, payload)
		if len(payloads) > len(replies) {
			t.Fatalf("unexpected extra chat request")
		}
		_, _ = w.Write([]byte(replies[len(payloads)-1]))
	}))
	defer server.Close()

	enableThinking := true
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 1, MaxNoProgressTurns: 2},
	}
	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "answer"}},
		llama.ChatOptions{Model: "model", EnableThinking: &enableThinking, PreserveThinking: true, MaxTokens: 128},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if answer != "Done." {
		t.Fatalf("unexpected answer %q", answer)
	}
	if len(payloads) != 2 {
		t.Fatalf("expected two model requests, got %d", len(payloads))
	}
	var foundPreserved bool
	for _, message := range payloads[1].Messages {
		if message.Role == "assistant" && message.ReasoningContent == "unfinished reasoning" {
			foundPreserved = true
			break
		}
	}
	if !foundPreserved {
		t.Fatalf("expected retry request to include empty reply reasoning_content, got %#v", payloads[1].Messages)
	}
}

func writeTestFile(root string, path string, content string) error {
	fullPath := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(fullPath, []byte(content), 0o644)
}

func TestRunnerExecutesReadOnlyChildTask(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "README.md", "route evidence\n"); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	replies := []string{
		"```tool\n{\"tool\":\"task\",\"agent\":\"explorer\",\"mode\":\"read_only\",\"prompt\":\"inspect README\"}\n```",
		"Child evidence: README.md mentions route evidence.",
		"Final: child evidence found in README.md.",
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request")
		}
		reply := replies[requests]
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))
	defer server.Close()

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 3, MaxNoProgressTurns: 2},
	}
	var sawTask bool
	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "delegate focused inspection"}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			if event.Result.Tool == "task" && strings.Contains(event.Result.Output, "Child evidence") {
				sawTask = true
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !sawTask {
		t.Fatal("expected task tool result with child output")
	}
	if !strings.Contains(answer, "Final") {
		t.Fatalf("unexpected answer: %q", answer)
	}
}

func TestReadOnlyChildTaskPermissionPolicyDeniesWritesAndAllowsReads(t *testing.T) {
	policy := readOnlyChildTaskPermissionPolicy()
	if decision := policy.Decide(workspace.ToolRequest{Tool: "edit_file", Path: "README.md"}); decision.Decision != PermissionDeny {
		t.Fatalf("edit_file decision = %q, want deny", decision.Decision)
	}
	if decision := policy.Decide(workspace.ToolRequest{Tool: "read_file", Path: "README.md"}); decision.Decision != PermissionAllow {
		t.Fatalf("read_file decision = %q, want allow", decision.Decision)
	}
	if decision := policy.Decide(workspace.ToolRequest{Tool: "task", Prompt: "nested"}); decision.Decision != PermissionDeny {
		t.Fatalf("task decision = %q, want deny", decision.Decision)
	}
}

func TestReadOnlyChildDoesNotAdvertiseNestedTaskOrWrites(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "README.md", "route evidence\n"); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	replies := []string{
		`{"tool":"task","agent":"explorer","mode":"read_only","prompt":"inspect README"}`,
		"Child evidence: README.md mentions route evidence.",
		"Final: child evidence found in README.md.",
	}
	type requestPayload struct {
		Tools []llama.Tool `json:"tools"`
	}
	var payloads []requestPayload
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request")
		}
		var payload requestPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		payloads = append(payloads, payload)
		reply := replies[requests]
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))
	defer server.Close()

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 3, MaxNoProgressTurns: 2},
	}
	if _, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "delegate focused inspection"}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(payloads) < 2 {
		t.Fatalf("expected parent and child requests, got %d", len(payloads))
	}
	childTools := payloads[1].Tools
	for _, tool := range childTools {
		switch tool.Function.Name {
		case "task", "task_ledger", "edit_file", "write_file", "append_file", "apply_patch", "mkdir":
			t.Fatalf("read-only child must not advertise %q; tools: %#v", tool.Function.Name, childTools)
		}
	}
}

func TestForcedCompletionDisablesToolAdvertisements(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "README.md", "hello\n"); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	replies := []string{
		`{"tool":"read_file","path":"README.md"}`,
		"Done from gathered evidence.",
	}
	type requestPayload struct {
		Tools      []llama.Tool `json:"tools,omitempty"`
		ToolChoice string       `json:"tool_choice,omitempty"`
	}
	var payloads []requestPayload
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request")
		}
		var payload requestPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		payloads = append(payloads, payload)
		reply := replies[requests]
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))
	defer server.Close()

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 1, MaxNoProgressTurns: 2},
	}
	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "inspect once then answer"}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if answer != "Done from gathered evidence." {
		t.Fatalf("unexpected answer: %q", answer)
	}
	if len(payloads) != 2 {
		t.Fatalf("expected initial plus forced completion request, got %d", len(payloads))
	}
	if len(payloads[1].Tools) != 0 || payloads[1].ToolChoice != "" {
		t.Fatalf("forced completion must be text-only, got tools=%d tool_choice=%q", len(payloads[1].Tools), payloads[1].ToolChoice)
	}
}

func TestLegacyParsedToolResultUsesToolChannel(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "README.md", "hello\n"); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	replies := []string{
		`{"tool":"read_file","path":"README.md"}`,
		"Read README.md.",
	}
	type requestMessage struct {
		Role       string           `json:"role"`
		Content    string           `json:"content,omitempty"`
		ToolCallID string           `json:"tool_call_id,omitempty"`
		ToolCalls  []llama.ToolCall `json:"tool_calls,omitempty"`
	}
	type requestPayload struct {
		Messages []requestMessage `json:"messages"`
	}
	var payloads []requestPayload
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request")
		}
		var payload requestPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		payloads = append(payloads, payload)
		reply := replies[requests]
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))
	defer server.Close()

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 2, MaxNoProgressTurns: 4},
	}
	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "read README"}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if answer != "Read README.md." {
		t.Fatalf("unexpected answer %q", answer)
	}
	if len(payloads) != 2 {
		t.Fatalf("expected two model requests, got %d", len(payloads))
	}
	messages := payloads[1].Messages
	var assistantToolCallID string
	var sawToolMessage bool
	for _, message := range messages {
		if message.Role == "assistant" && len(message.ToolCalls) > 0 {
			assistantToolCallID = message.ToolCalls[0].ID
			if message.Content != "" {
				t.Fatalf("legacy tool-call assistant message should not preserve JSON content, got %q", message.Content)
			}
		}
		if message.Role == "tool" {
			sawToolMessage = true
			if assistantToolCallID == "" {
				t.Fatalf("tool message appeared before assistant tool_call: %#v", messages)
			}
			if message.ToolCallID != assistantToolCallID {
				t.Fatalf("tool_call_id = %q, want %q", message.ToolCallID, assistantToolCallID)
			}
			if !strings.Contains(message.Content, "README.md") {
				t.Fatalf("tool message did not contain read result, got %q", message.Content)
			}
		}
		if message.Role == "system" && strings.Contains(message.Content, "README.md") && strings.Contains(message.Content, "Tool") {
			t.Fatalf("legacy tool result leaked through system role: %#v", message)
		}
	}
	if !sawToolMessage {
		t.Fatalf("expected legacy parsed tool result to use role=tool, messages=%#v", messages)
	}
}

func TestLegacyMalformedToolRequestUsesToolChannel(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "README.md", "hello\n"); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	malformed := `{"tool":"edit_file","path":"README.md","old_text":`
	replies := []string{
		malformed,
		"Blocked by malformed request.",
	}
	type requestMessage struct {
		Role       string           `json:"role"`
		Content    string           `json:"content,omitempty"`
		ToolCallID string           `json:"tool_call_id,omitempty"`
		ToolCalls  []llama.ToolCall `json:"tool_calls,omitempty"`
	}
	type requestPayload struct {
		Messages []requestMessage `json:"messages"`
	}
	var payloads []requestPayload
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request")
		}
		var payload requestPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		payloads = append(payloads, payload)
		reply := replies[requests]
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))
	defer server.Close()

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		Policy: RunPolicy{MaxToolSteps: 2, MaxNoProgressTurns: 4},
	}
	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "edit README"}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if answer != "Blocked by malformed request." {
		t.Fatalf("unexpected answer %q", answer)
	}
	if len(payloads) != 2 {
		t.Fatalf("expected two model requests, got %d", len(payloads))
	}
	messages := payloads[1].Messages
	var assistantToolCallID string
	var sawToolMessage bool
	for _, message := range messages {
		if message.Role == "assistant" && len(message.ToolCalls) > 0 {
			assistantToolCallID = message.ToolCalls[0].ID
			if message.ToolCalls[0].Function.Name != "tool_parse" {
				t.Fatalf("malformed legacy request should be recorded as tool_parse, got %#v", message.ToolCalls[0])
			}
			var args map[string]string
			if err := json.Unmarshal([]byte(message.ToolCalls[0].Function.Arguments), &args); err != nil {
				t.Fatalf("tool_parse synthetic arguments should be valid JSON, got %q: %v", message.ToolCalls[0].Function.Arguments, err)
			}
			if !strings.Contains(args["raw"], "edit_file") {
				t.Fatalf("tool_parse synthetic arguments should retain raw malformed request, got %#v", args)
			}
			if message.Content != "" {
				t.Fatalf("legacy parse-failure assistant message should not preserve malformed content, got %q", message.Content)
			}
		}
		if message.Role == "tool" {
			sawToolMessage = true
			if assistantToolCallID == "" {
				t.Fatalf("tool message appeared before assistant tool_call: %#v", messages)
			}
			if message.ToolCallID != assistantToolCallID {
				t.Fatalf("tool_call_id = %q, want %q", message.ToolCallID, assistantToolCallID)
			}
			if !strings.Contains(message.Content, "Malformed tool request rejected") {
				t.Fatalf("tool message did not contain parse failure, got %q", message.Content)
			}
		}
		if message.Role == "system" && strings.Contains(message.Content, "edit_file") {
			t.Fatalf("malformed tool request leaked through system role: %#v", message)
		}
	}
	if !sawToolMessage {
		t.Fatalf("expected legacy parse failure to use role=tool, messages=%#v", messages)
	}
}

func TestRunnerInjectsInitialRuntimeBriefIntoModelMessages(t *testing.T) {
	type chatMessage struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	type requestPayload struct {
		Messages []chatMessage `json:"messages"`
	}
	var payload requestPayload
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": "Done."},
			}},
		})
	}))
	defer server.Close()

	runner := &Runner{
		Client:       llama.NewClient(server.URL, time.Second),
		Workspace:    workspace.NewManager(config.Workspace{Root: t.TempDir(), CWD: "."}),
		Policy:       RunPolicy{MaxToolSteps: 1, MaxNoProgressTurns: 1},
		RuntimeBrief: workerruntime.RuntimeBriefForRole("implement", "main", "update README"),
	}
	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "update readme"}}, llama.ChatOptions{}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if answer != "Done." {
		t.Fatalf("unexpected answer: %q", answer)
	}
	var sawBrief bool
	for _, msg := range payload.Messages {
		if msg.Role == "system" && strings.Contains(msg.Content, "[frontier step instruction]") && strings.Contains(msg.Content, `"objective":"update README"`) {
			sawBrief = true
			break
		}
	}
	if !sawBrief {
		t.Fatalf("expected initial runtime brief in request messages, got %#v", payload.Messages)
	}
}

func TestRunnerInjectsVerificationBootstrapInstruction(t *testing.T) {
	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: t.TempDir(), CWD: "."}),
		Policy:    RunPolicy{MaxToolSteps: 1, MaxNoProgressTurns: 1},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			PermissionProfile: "read_only_verification_shell",
			VerificationPlan:  []string{"go test ./..."},
		},
	}
	bootstrap := runner.initialBootstrapInstruction()
	if !strings.Contains(bootstrap, "[verification bootstrap]") || !strings.Contains(bootstrap, "Configured verification command: go test ./...") {
		t.Fatalf("expected verification bootstrap instruction, got %q", bootstrap)
	}
}

func TestVerificationOnlyAutoRunsShellWithoutModelRoundTrip(t *testing.T) {
	root := t.TempDir()
	runner := &Runner{
		Client: llama.NewClient("http://127.0.0.1:1", time.Millisecond),
		Workspace: workspace.NewManager(config.Workspace{
			Root:                  root,
			CWD:                   root,
			EnableShell:           true,
			CommandTimeoutSeconds: 2,
		}),
		Policy: RunPolicy{MaxToolSteps: 1, MaxNoProgressTurns: 1},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			PermissionProfile: "read_only_verification_shell",
			AllowedTools:      []string{"shell", "read_file", "search_files", "search_content"},
			VerificationPlan:  []string{"go test ./..."},
		},
	}
	var requested []workspace.ToolRequest
	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "run tests"}},
		llama.ChatOptions{},
		nil,
		func(event ToolRequestEvent) {
			requested = append(requested, event.Request)
		},
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(requested) != 1 || requested[0].Tool != "shell" || requested[0].Command != "go test ./..." {
		t.Fatalf("expected deterministic verification shell request, got %#v", requested)
	}
	if !strings.Contains(answer, "Verification failed.") || !strings.Contains(answer, "No files were edited.") {
		t.Fatalf("expected synthesized verification failure answer, got:\n%s", answer)
	}
}

func TestVerificationOnlyRecoveryNudgeSuggestsVerificationCommand(t *testing.T) {
	nudge := buildRejectionRecoveryNudge(
		workspace.ToolRequest{Tool: "shell", Command: "pwd"},
		nil,
		nil,
		workerruntime.WorkerRuntimeBrief{
			PermissionProfile: "read_only_verification_shell",
			VerificationPlan:  []string{"go test ./..."},
		},
		"main",
		false,
		false,
	)
	if !strings.Contains(nudge, "configured plan starts with `go test ./...`") {
		t.Fatalf("expected verification command in recovery nudge, got:\n%s", nudge)
	}
	if strings.Contains(nudge, `{"tool":"search_files"`) || strings.Contains(nudge, `{"tool":"search_content"`) {
		t.Fatalf("verification recovery nudge should not suggest discovery first, got:\n%s", nudge)
	}
}

func TestImplementationRecoveryNudgeSuggestsVerificationAfterWrites(t *testing.T) {
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read README.md lines 1-3", Output: "one\ntwo\nthree"},
		{Tool: "edit_file", Summary: "Edited README.md lines 1-1"},
		{Tool: "edit_file", Summary: "Edited README.md lines 2-2"},
		{
			Tool:    "read_file",
			Summary: "Verification required after workspace edits",
			Error:   "the workspace has 2 successful write(s) since the last verification.",
		},
	}
	nudge := buildRejectionRecoveryNudge(
		workspace.ToolRequest{Tool: "read_file", Path: "README.md"},
		results,
		nil,
		workerruntime.WorkerRuntimeBrief{
			TaskFamily:           "implementation",
			VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
		},
		"main",
		false,
		false,
	)
	if !strings.Contains(nudge, "pending verification") ||
		!strings.Contains(nudge, "git diff --check") {
		t.Fatalf("expected verification-first nudge, got:\n%s", nudge)
	}
	if strings.Contains(nudge, "smaller window") || strings.Contains(nudge, "structured probe") {
		t.Fatalf("verification recovery nudge should not suggest more inspection, got:\n%s", nudge)
	}
}

func TestExactAnchorPreWriteRecoveryNudgeDoesNotSuggestReadRefinement(t *testing.T) {
	nudge := buildRejectionRecoveryNudge(
		workspace.ToolRequest{Tool: "read_file", Path: "scripts/ui/depot_panel.gd"},
		nil,
		nil,
		workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			Constraints:       []string{"exact_anchor_implementation"},
			RequiredEditPaths: []string{"scripts/ui/depot_panel.gd"},
			TargetRefs:        []string{"scripts/ui/depot_panel.gd"},
		},
		"main",
		false,
		false,
	)
	for _, want := range []string{"exact-anchor implementation pre-write", "Exact-anchor write capability", "<<QUESTION>>"} {
		if !strings.Contains(nudge, want) {
			t.Fatalf("expected exact-anchor recovery nudge to contain %q, got:\n%s", want, nudge)
		}
	}
	for _, forbidden := range []string{"smaller window", "structured probe"} {
		if strings.Contains(nudge, forbidden) {
			t.Fatalf("exact-anchor recovery nudge should not suggest %q, got:\n%s", forbidden, nudge)
		}
	}
}

func TestRequiredPathRecoveryNudgeDoesNotAnswerFirst(t *testing.T) {
	results := []workspace.ToolResult{
		{Tool: "edit_file", Summary: "Edited native/src/energy.cpp"},
	}
	nudge := buildRejectionRecoveryNudge(
		workspace.ToolRequest{Tool: "read_file", Path: "native/tests/test_energy.cpp"},
		results,
		nil,
		workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{
				"native/src/energy.cpp",
				"native/tests/test_energy.cpp",
			},
		},
		"main",
		false,
		false,
	)
	for _, want := range []string{"required edit path", "native/tests/test_energy.cpp", "Required outcomes are an allowed write"} {
		if !strings.Contains(nudge, want) {
			t.Fatalf("expected required-path recovery nudge to contain %q, got:\n%s", want, nudge)
		}
	}
	for _, forbidden := range []string{"Answer with the evidence already gathered", "Only take one narrow next step", "smaller window", "structured probe"} {
		if strings.Contains(nudge, forbidden) {
			t.Fatalf("required-path recovery nudge should not suggest %q, got:\n%s", forbidden, nudge)
		}
	}
}

func TestTargetInspectionRecoveryNudgeStaysInsideTarget(t *testing.T) {
	results := []workspace.ToolResult{
		{Tool: "go_file_outline", Summary: "Outlined 15 top-level declaration(s) in internal/app/turn_planner.go.", Output: "package app"},
		{Tool: "read_file", Summary: "Read f1 docs/frontier-routing-implementation-plan.md lines 1-220", Output: "doc excerpt"},
		{Tool: "read_file", Summary: "Read f1 docs/frontier-routing-implementation-plan.md lines 220-359", Output: "doc excerpt 2"},
	}
	nudge := buildRejectionRecoveryNudge(
		workspace.ToolRequest{Tool: "go_read_symbol", Path: "internal/app/turn_planner.go", Pattern: "turnRoutePlan"},
		results,
		nil,
		workerruntime.WorkerRuntimeBrief{
			TaskFamily: "explore_then_implement",
			TargetRefs: []string{"docs/frontier-routing-implementation-plan.md"},
		},
		"main",
		false,
		false,
	)
	if !strings.Contains(nudge, "already has source evidence and target-file evidence") {
		t.Fatalf("expected target-inspection guidance, got:\n%s", nudge)
	}
	if !strings.Contains(nudge, "Relevant target window: docs/frontier-routing-implementation-plan.md lines 340-359") {
		t.Fatalf("expected narrow target window, got:\n%s", nudge)
	}
	if strings.Contains(nudge, `{"tool":"search_files"`) || strings.Contains(nudge, `{"tool":"search_content","pattern":"TODO|FIXME"`) {
		t.Fatalf("target-inspection recovery nudge should not suggest broad discovery, got:\n%s", nudge)
	}
}

func TestPrematureTargetReadRecoveryNudgeSuggestsSourceEvidence(t *testing.T) {
	results := []workspace.ToolResult{
		{Tool: "go_workspace_symbol", Summary: "Resolved Go workspace symbols with gopls", Output: "internal/app/turn_planner.go:4 turnRoutePlan\ninternal/app/model_state.go:10 routeState\n"},
	}
	nudge := buildRejectionRecoveryNudge(
		workspace.ToolRequest{Tool: "read_file", Path: "docs/frontier-routing-implementation-plan.md"},
		results,
		nil,
		workerruntime.WorkerRuntimeBrief{
			TaskFamily: "explore_then_implement",
			TargetRefs: []string{"docs/frontier-routing-implementation-plan.md"},
		},
		"main",
		false,
		false,
	)
	if !strings.Contains(nudge, "This turn still needs source evidence before the named target file.") {
		t.Fatalf("expected source-evidence guidance, got:\n%s", nudge)
	}
	if !strings.Contains(nudge, "internal/app/turn_planner.go") {
		t.Fatalf("expected non-target source path, got:\n%s", nudge)
	}
	if strings.Contains(nudge, `{"tool":"read_file","path":"docs/frontier-routing-implementation-plan.md"`) {
		t.Fatalf("premature target recovery nudge should not suggest reading the target again, got:\n%s", nudge)
	}
}

func TestClosureRecoveryNudgeAvoidsBroadRediscoveryAfterReadiness(t *testing.T) {
	results := []workspace.ToolResult{{
		Tool:    "discovery_query",
		Summary: "Ran architecture_boundary discovery query",
		Output:  `{"artifact_path":".idiom/discovery/queries/query.json","evidence":[{"path":"internal/agent/runner.go","excerpt":"result := r.runChildTask(ctx, req, opts)"},{"path":"internal/agent/runner_child_tasks.go","excerpt":"You are a read-only delegated child task."}]}`,
	}}
	nudge := buildRejectionRecoveryNudge(
		workspace.ToolRequest{Tool: "read_file", Path: "internal/agent/runner.go"},
		results,
		nil,
		workerruntime.WorkerRuntimeBrief{
			TaskFamily: "delegated_read_only",
			Objective:  "Explain how this harness uses subagents.",
		},
		"child:explorer",
		true,
		false,
	)
	for _, want := range []string{"closure is already justified", "Answer now from the evidence already gathered", "one detail remains uncertain", "do not request more tools"} {
		if !strings.Contains(nudge, want) {
			t.Fatalf("expected closure recovery nudge to contain %q, got:\n%s", want, nudge)
		}
	}
	for _, forbidden := range []string{`{"tool":"search_files"`, `{"tool":"search_content","pattern":"TODO|FIXME"`} {
		if strings.Contains(nudge, forbidden) {
			t.Fatalf("closure recovery nudge should not reopen broad discovery with %q, got:\n%s", forbidden, nudge)
		}
	}
}

func TestFailureFeedbackPacketKeepsRepeatedActionClosureBiased(t *testing.T) {
	results := []workspace.ToolResult{{
		Tool:    "discovery_query",
		Summary: "Ran architecture_boundary discovery query",
		Output:  `{"artifact_path":".idiom/discovery/queries/query.json","evidence":[{"path":"internal/agent/runner.go","excerpt":"result := r.runChildTask(ctx, req, opts)"},{"path":"internal/agent/runner_child_tasks.go","excerpt":"You are a read-only delegated child task."}]}`,
	}}
	req := workspace.ToolRequest{Tool: "read_file", Path: "internal/agent/answer_composer.go"}
	result := workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Repeated tool request rejected",
		Error:   repeatedClosureRequestErrorText("delegated_read_only_answer_ready"),
	}
	packet := buildRunnerFailureFeedbackPacket(
		"child:explorer",
		3,
		"tool_rejected",
		req,
		result,
		results,
		false,
		workerruntime.WorkerRuntimeBrief{
			TaskFamily: "delegated_read_only",
			Objective:  "Explain how this harness uses subagents.",
		},
	)
	joined := strings.Join(append(append([]string{}, packet.RequiredNext...), packet.ValidNext...), "\n")
	for _, want := range []string{
		"answer now from the evidence already gathered",
		"one exact symbol, discovered handle, or narrow range",
		"materially wrong or missing if you answered now",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected closure-biased failure feedback to contain %q, got %#v", want, packet)
		}
	}
	for _, forbidden := range []string{"search_files on a discovered path or pattern", "use a targeted read-only shell search"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("failure feedback should not reintroduce broad fallback %q, got %#v", forbidden, packet)
		}
	}
}

func TestVerificationOnlyRedirectsPreCheckExplorationToVerificationCommand(t *testing.T) {
	root := t.TempDir()
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": `{"tool":"search_files","pattern":"*.go","path":"."}`},
			}},
		})
	}))
	defer server.Close()

	var requested []workspace.ToolRequest
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:                  root,
			CWD:                   root,
			EnableShell:           true,
			CommandTimeoutSeconds: 2,
		}),
		Policy: RunPolicy{MaxToolSteps: 3, MaxNoProgressTurns: 2},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			PermissionProfile: "read_only_verification_shell",
			AllowedTools:      []string{"shell", "read_file", "search_files", "search_content"},
			VerificationPlan:  []string{"go test ./..."},
		},
	}
	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "run tests and report failure"}},
		llama.ChatOptions{},
		nil,
		func(event ToolRequestEvent) {
			requested = append(requested, event.Request)
		},
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if requests != 0 {
		t.Fatalf("verification-only run should not ask the model before running shell, got %d request(s)", requests)
	}
	if !strings.Contains(answer, "Verification failed.") {
		t.Fatalf("expected synthesized verification answer, got %q", answer)
	}
	if len(requested) == 0 {
		t.Fatalf("expected at least one tool request")
	}
	if requested[0].Tool != "shell" || requested[0].Command != "go test ./..." || requested[0].Workdir != "." {
		t.Fatalf("expected first tool request to be redirected verification command, got %#v", requested[0])
	}
}

func TestDebugLogEvidenceSatisfiedAfterSingleHitAndReadWindow(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{TaskFamily: "debug_logs"}
	results := []workspace.ToolResult{
		{
			Tool:    "search_content",
			Summary: "Found 1 matching line at logs/llama-server.log:247005.",
			Output:  "f3 logs/llama-server.log:247005:request exceeds context\ncontext:\n247004:foo",
		},
		{
			Tool:    "read_file",
			Summary: "Read f3 logs/llama-server.log lines 247000-247079 (file has 268859 lines total; open-ended log range capped to 80 lines; pass an explicit end_line for a different bounded window)",
			Output:  "247000:...\n247005:request exceeds context",
		},
	}
	if !debugLogEvidenceSatisfied(brief, results) {
		t.Fatalf("expected debug-log evidence to be satisfied for %#v", results)
	}
}

func TestDebugLogFollowupRedirectsSearchToHitWindow(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{TaskFamily: "debug_logs"}
	results := []workspace.ToolResult{{
		Tool:    "search_content",
		Summary: "Found 1 matching line at logs/llama-server.log:247005.",
		Output:  "f3 logs/llama-server.log:247005:request exceeds context",
	}}
	redirected, ok := debugLogFollowupRequest(brief, results, workspace.ToolRequest{
		Tool:    "search_content",
		Pattern: "send_error",
		Path:    "logs",
		Glob:    "llama-server.log",
	})
	if !ok {
		t.Fatal("expected redirect for repeated search after concrete log hit")
	}
	if redirected.Tool != "read_file" || redirected.ID != "f3" {
		t.Fatalf("unexpected redirect target: %#v", redirected)
	}
	if redirected.StartLine > 247005 || redirected.EndLine < 247005 {
		t.Fatalf("redirected window must cover hit line, got %#v", redirected)
	}
}

func TestDebugLogPreflightRedirectsGenericSearchToNewestLogDiscovery(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "logs/conversations/recent.jsonl", "{\"event\":\"x\"}\n"); err != nil {
		t.Fatalf("write log file: %v", err)
	}

	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{
			Root:                root,
			CWD:                 root,
			MaxDirectoryEntries: 64,
		}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{TaskFamily: "debug_logs"},
	}
	redirected, ok := runner.debugLogPreflightRequest(nil, workspace.ToolRequest{
		Tool:    "search_files",
		Pattern: "*log*",
	})
	if !ok {
		t.Fatal("expected redirect to newest-log discovery")
	}
	if redirected.Tool != "search_files" || redirected.Pattern != "logs/**/*" {
		t.Fatalf("unexpected redirect request: %#v", redirected)
	}
}

func TestDebugLogPreflightRedirectsToSelectedLogTail(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "logs/conversations/recent.jsonl", "{\"event\":\"x\"}\n"); err != nil {
		t.Fatalf("write log file: %v", err)
	}
	mgr := workspace.NewManager(config.Workspace{
		Root:                root,
		CWD:                 root,
		MaxDirectoryEntries: 64,
	})
	search := mgr.Execute(context.Background(), workspace.ToolRequest{
		Tool:    "search_files",
		Pattern: "logs/**/*",
		Path:    ".",
	})
	if search.Error != "" {
		t.Fatalf("search_files failed: %#v", search)
	}

	runner := &Runner{
		Workspace:    mgr,
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{TaskFamily: "debug_logs"},
	}
	redirected, ok := runner.debugLogPreflightRequest([]workspace.ToolResult{search}, workspace.ToolRequest{
		Tool:    "search_content",
		Pattern: "chosen_tool",
		Glob:    "*.go",
	})
	if !ok {
		t.Fatal("expected redirect to selected-log tail read")
	}
	if redirected.Tool != "read_file" {
		t.Fatalf("expected read_file redirect, got %#v", redirected)
	}
	if redirected.ID == "" && redirected.Path != "logs/conversations/recent.jsonl" {
		t.Fatalf("expected selected log target, got %#v", redirected)
	}
}

func TestDebugLogPhaseScopeRejectsSourceDriftAfterTail(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "logs/conversations/recent.jsonl", strings.Repeat("{\"event\":\"x\"}\n", 20)); err != nil {
		t.Fatalf("write log file: %v", err)
	}
	mgr := workspace.NewManager(config.Workspace{
		Root:                root,
		CWD:                 root,
		MaxDirectoryEntries: 64,
		MaxFileBytes:        1 << 20,
	})
	search := mgr.Execute(context.Background(), workspace.ToolRequest{
		Tool:    "search_files",
		Pattern: "logs/**/*",
		Path:    ".",
	})
	if search.Error != "" {
		t.Fatalf("search_files failed: %#v", search)
	}
	selection, ok := parseDebugLogFileSelection(search)
	if !ok {
		t.Fatalf("expected search result to expose a log selection: %#v", search)
	}
	readReq := debugLogTailRequest(selection)
	read := mgr.Execute(context.Background(), readReq)
	if read.Error != "" {
		t.Fatalf("read_file failed: %#v", read)
	}
	results := []workspace.ToolResult{search, read}
	brief := workerruntime.WorkerRuntimeBrief{TaskFamily: "debug_logs"}

	if _, _, reject := debugLogPhaseScopeRejection(workspace.ToolRequest{
		Tool:    "search_content",
		Pattern: "chosen_tool",
		Glob:    "*.go",
	}, results, brief); !reject {
		t.Fatal("expected source drift to be rejected after selected-log tail read")
	}
	if _, _, reject := debugLogPhaseScopeRejection(workspace.ToolRequest{
		Tool: "read_file",
		ID:   selection.Handle,
	}, results, brief); !reject {
		t.Fatal("expected blind reread of selected log to be rejected")
	}
	if _, _, reject := debugLogPhaseScopeRejection(workspace.ToolRequest{
		Tool:      "read_file",
		ID:        selection.Handle,
		Path:      selection.Path,
		StartLine: 5,
		EndLine:   20,
	}, results, brief); !reject {
		t.Fatal("expected second selected-log read without a concrete hit to be rejected")
	}
}

func TestForcedCompletionTreatsXMLToolMarkupAsToolRequest(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "README.md", "hello\n"); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	replies := []string{
		`{"tool":"read_file","path":"README.md"}`,
		"<tool_call>\n<function=read_file>\n<parameter=path>README.md</parameter>\n</function>\n</tool_call>",
		"README.md was inspected and contains the gathered evidence needed for this answer.",
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request")
		}
		reply := replies[requests]
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))
	defer server.Close()

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 1, MaxNoProgressTurns: 2},
	}
	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "inspect once then answer"}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Contains(answer, "<tool_call>") || !strings.Contains(answer, "README.md was inspected") {
		t.Fatalf("expected repaired prose answer instead of XML tool markup, got:\n%s", answer)
	}
	if strings.Contains(answer, "Partial findings") {
		t.Fatalf("expected normal evidence answer without partial-findings banner, got:\n%s", answer)
	}
	if requests != len(replies) {
		t.Fatalf("expected invalid final answer retry, got %d requests", requests)
	}
}

func TestForcedCompletionRepairsTextualJSONToolRequestBeforeFallback(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "README.md", "hello\n"); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	replies := []string{
		`{"tool":"read_file","path":"README.md"}`,
		`{"tool":"read_file","path":"README.md","named_gap":"read more before answering"}`,
		"The harness should answer from the evidence already gathered instead of requesting another tool.",
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request")
		}
		reply := replies[requests]
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))
	defer server.Close()

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 1, MaxNoProgressTurns: 2},
	}
	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "inspect once then answer"}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if answer != replies[2] {
		t.Fatalf("expected repaired prose answer before deterministic fallback, got:\n%s", answer)
	}
	if requests != len(replies) {
		t.Fatalf("expected invalid final answer retry, got %d requests", requests)
	}
}

func TestForcedCompletionRepairsToolRequestBeforeDeterministicEvidenceFallback(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "internal/agent/runner_child_tasks.go", "package agent\n\nfunc runChildTask() {}\n"); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	repaired := "The harness uses sub-agents by delegating read-only exploration through the task pathway, then folding the child result back into the parent runner as evidence for the final answer."
	replies := []string{
		`{"tool":"read_file","path":"internal/agent/runner_child_tasks.go","named_gap":"body of runChildTask in internal/agent/runner_child_tasks.go","why_not_answering":"need one anchored read before answering"}`,
		`{"tool":"go_read_symbol","path":"internal/agent/runner_child_tasks.go","pattern":"Runner.runChildTask","named_gap":"read runChildTask before answering"}`,
		repaired,
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request")
		}
		reply := replies[requests]
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))
	defer server.Close()

	var modelCallEvents []string
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 1, MaxNoProgressTurns: 2},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "architecture",
			Contract:          "architecture_assessment",
			Objective:         "how does this harness utilize sub-agents?",
			PermissionProfile: "read_only_workspace",
			Constraints:       []string{"child_packet_available"},
		},
		SeedResults: []workspace.ToolResult{{
			Tool:    "task",
			Summary: "Child task completed",
			Output:  "Question: how does this harness utilize sub-agents?\nSource: delegated read-only evidence packet.\n\nPrimary files: `internal/agent/runner.go`, `internal/agent/runner_child_tasks.go`.\n\nKey findings:\n- The parent runner delegates exploration to a child task and uses the child output as evidence.",
		}},
		OnEvent: func(event RunEvent) {
			if event.Role == "model_call" {
				modelCallEvents = append(modelCallEvents, event.Content)
			}
		},
	}
	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "how does this harness utilize sub-agents?"}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(answer, "internal/agent/runner_child_tasks.go") || !strings.Contains(strings.ToLower(answer), "child task") {
		t.Fatalf("expected deterministic fallback to preserve the child-task finding and relevant implementation surface, got:\n%s", answer)
	}
	for _, forbidden := range []string{"The strongest implementation evidence is in", "What this establishes:", "Source: delegated read-only evidence packet", "Primary files:"} {
		if strings.Contains(answer, forbidden) {
			t.Fatalf("expected cleaned deterministic prose instead of packet metadata %q:\n%s", forbidden, answer)
		}
	}
	if requests != 2 {
		t.Fatalf("expected deterministic fallback to avoid invalid-final retry, got %d requests", requests)
	}
	for _, event := range modelCallEvents {
		if strings.Contains(event, `"label":"invalid_final_answer_retry"`) {
			t.Fatalf("did not expect invalid_final_answer_retry when deterministic child-packet fallback is available, got %#v", modelCallEvents)
		}
	}
}

func TestForcedCompletionRepairsRepeatedToolRequestsWithFinalSynthesis(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "internal/agent/runner_child_tasks.go", "package agent\n\nfunc runChildTask() {}\n"); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	repaired := "The harness uses sub-agents by launching a bounded read-only child runner through `internal/agent/runner_child_tasks.go`, then returning that child result to the parent as evidence for the final response."
	replies := []string{
		`{"tool":"read_file","path":"internal/agent/runner_child_tasks.go","named_gap":"body of runChildTask in internal/agent/runner_child_tasks.go","why_not_answering":"need one anchored read before answering"}`,
		`{"tool":"go_read_symbol","path":"internal/agent/runner_child_tasks.go","pattern":"Runner.runChildTask","named_gap":"read runChildTask before answering"}`,
		`{"tool":"read_file","path":"internal/agent/runner_child_tasks.go","start_line":1,"end_line":80,"named_gap":"read runChildTask before answering","why_not_answering":"need another probe"}`,
		repaired,
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request")
		}
		reply := replies[requests]
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))
	defer server.Close()

	var modelCallEvents []string
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 1, MaxNoProgressTurns: 2},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "architecture",
			Contract:          "architecture_assessment",
			Objective:         "how does this harness utilize sub-agents?",
			PermissionProfile: "read_only_workspace",
			Constraints:       []string{"child_packet_available"},
		},
		SeedResults: []workspace.ToolResult{{
			Tool:    "task",
			Summary: "Child task completed",
			Output:  "The strongest implementation evidence is in `internal/agent/runner_child_tasks.go`. Use those findings as the source of truth for `how does this harness utilize sub-agents`.\n\nWhat this establishes:\n- Text anchors: Found matching implementation paths.",
		}},
		OnEvent: func(event RunEvent) {
			if event.Role == "model_call" {
				modelCallEvents = append(modelCallEvents, event.Content)
			}
		},
	}
	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "how does this harness utilize sub-agents?"}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(answer, "internal/agent/runner_child_tasks.go") {
		t.Fatalf("expected deterministic fallback to preserve the relevant implementation surface, got:\n%s", answer)
	}
	for _, forbidden := range []string{"The strongest implementation evidence is in", "What this establishes:", "Source: delegated read-only evidence packet", "Primary files:"} {
		if strings.Contains(answer, forbidden) {
			t.Fatalf("expected cleaned fallback prose instead of internal evidence framing %q:\n%s", forbidden, answer)
		}
	}
	if requests != 4 {
		t.Fatalf("expected forced completion, invalid-final retry, and repaired fallback answer, got %d requests", requests)
	}
	if !slices.ContainsFunc(modelCallEvents, func(event string) bool { return strings.Contains(event, `"label":"invalid_final_answer_retry"`) }) {
		t.Fatalf("expected model call label %q, got %#v", `"label":"invalid_final_answer_retry"`, modelCallEvents)
	}
	if !slices.ContainsFunc(modelCallEvents, func(event string) bool { return strings.Contains(event, `"label":"fallback_final_answer_retry"`) }) {
		t.Fatalf("expected fallback_final_answer_retry when the deterministic fallback is still internal-facing, got %#v", modelCallEvents)
	}
}

func TestForcedCompletionRepairsReadOnlyFallbackAnswer(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "internal/agent/runner_child_tasks.go", "package agent\n\nfunc seed() {}\n"); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	repaired := "The harness uses sub-agents through `Runner.runChildTask` in `internal/agent/runner_child_tasks.go`. That path normalizes the delegated prompt, creates a child `Runner` with a bounded read-only policy and child-scoped permissions, runs the child task, and then returns the child answer/results to the parent as grounded evidence for the final response."
	replies := []string{
		`{"tool":"go_read_symbol","path":"internal/agent/runner_child_tasks.go","pattern":"Runner.runWorkbranchChildTask","named_gap":"need the write-enabled variant before answering","why_not_answering":"need one more child-mode comparison"}`,
		`{"tool":"read_file","path":"internal/agent/runner_child_tasks.go","start_line":326,"end_line":420,"named_gap":"need the write-enabled variant before answering","why_not_answering":"need one more child-mode comparison"}`,
		repaired,
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request")
		}
		reply := replies[requests]
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))
	defer server.Close()

	var modelCallEvents []string
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 0, MaxNoProgressTurns: 2},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "architecture",
			Contract:          "architecture_assessment",
			Objective:         "how does this harness make use of sub-agents?",
			PermissionProfile: "read_only_workspace",
		},
		SeedResults: []workspace.ToolResult{
			{
				Tool:    "discovery_query",
				Summary: "Ran architecture_boundary discovery query with 4 layer(s)",
				Output:  `{"artifact_path":".idiom/discovery/queries/query.json","evidence":[{"name":"text_anchor_search","summary":"Found child-runner implementation paths.","output_excerpt":"internal/agent/runner_child_tasks.go:16: func (r *Runner) runChildTask(ctx context.Context, req workspace.ToolRequest, opts llama.ChatOptions) workspace.ToolResult"}]}`,
			},
			{
				Tool:    "go_read_symbol",
				Summary: "Read Runner.runChildTask in internal/agent/runner_child_tasks.go lines 16-132",
				Output:  "func (r *Runner) runChildTask(ctx context.Context, req workspace.ToolRequest, opts llama.ChatOptions) workspace.ToolResult {\n\tprompt = normalizeReadOnlyChildPrompt(prompt)\n\tprompt = r.augmentReadOnlyChildPrompt(prompt)\n\tchild := &Runner{\n\t\tClient: r.Client,\n\t\tWorkspace: r.Workspace,\n\t\tPolicy: childPolicy,\n\t\tAgentName: \"child:\" + agentName,\n\t\tPermissionPolicy: readOnlyChildTaskPermissionPolicy(),\n\t\tDisableTaskTool: true,\n\t}\n\tanswer, err := child.Run(ctx, []llama.ChatMessage{{Role: \"user\", Content: prompt}}, opts, nil, nil, nil)\n\tanswer = augmentDelegatedReadOnlyPacket(answer, prompt, childResults)\n\treturn workspace.ToolResult{Tool: \"task\", Summary: \"Child task completed\", Output: strings.TrimSpace(answer)}\n}\n",
			},
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "model_call" {
				modelCallEvents = append(modelCallEvents, event.Content)
			}
		},
	}
	answer, err := runner.completeWithoutMoreToolsWithReason(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "how does this harness make use of sub-agents?"}},
		llama.ChatOptions{},
		runner.SeedResults,
		"Post-readiness named-gap contract is exhausted. Tools are now disabled. Write the normal final answer from the evidence already gathered.",
		nil,
	)
	if err != nil {
		t.Fatalf("completeWithoutMoreToolsWithReason: %v", err)
	}
	if answer != repaired {
		t.Fatalf("expected repaired user-facing answer, got:\n%s", answer)
	}
	for _, forbidden := range []string{"The strongest implementation evidence is in", "What this establishes:", "Source: delegated read-only evidence packet", "Inspected paths:"} {
		if strings.Contains(answer, forbidden) {
			t.Fatalf("expected repaired answer without internal evidence framing %q:\n%s", forbidden, answer)
		}
	}
	if !slices.ContainsFunc(modelCallEvents, func(event string) bool { return strings.Contains(event, `"label":"fallback_final_answer_retry"`) }) {
		t.Fatalf("expected fallback_final_answer_retry for internal fallback repair, got %#v", modelCallEvents)
	}
	if requests != len(replies) {
		t.Fatalf("expected forced completion, invalid-final retry, and repair, used %d/%d replies", requests, len(replies))
	}
}

func TestBudgetClosureRunEmitsAcceptanceSignals(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "README.md", "line one\nline two\n"); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	replies := []string{
		`{"tool":"read_file","path":"README.md","start_line":1,"end_line":2}`,
		"README.md currently has two lines and no sub-agent behavior in it.",
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request")
		}
		reply := replies[requests]
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))
	defer server.Close()

	var roles []string
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 1, MaxNoProgressTurns: 2},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "architecture",
			Contract:          "architecture_assessment",
			Objective:         "What is in README.md?",
			PermissionProfile: "read_only_workspace",
		},
		OnEvent: func(event RunEvent) {
			roles = append(roles, event.Role)
		},
	}
	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "What is in README.md?"}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(answer, "two lines") {
		t.Fatalf("expected forced completion answer, got %q", answer)
	}
	for _, want := range []string{"readiness_check", "final_answer_critic"} {
		if !slices.Contains(roles, want) {
			t.Fatalf("expected final acceptance signal %q, got roles %v", want, roles)
		}
	}
	if requests != len(replies) {
		t.Fatalf("expected one tool turn plus one budget-closure answer, used %d/%d replies", requests, len(replies))
	}
}

func TestForcedCompletionUsesDeterministicMutationFallback(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": "Changed paths:\n- src/example.cpp\n\nVerification: git diff --check passed."},
			}},
		})
	}))
	defer server.Close()

	root := t.TempDir()
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:           "implementation",
			Contract:             "implementation_result",
			PermissionProfile:    "guarded_workspace_write",
			VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
			Constraints:          []string{"planner_owned_verification"},
			RequiredEditPaths:    []string{"src/example.h", "src/example.cpp"},
		},
	}
	results := []workspace.ToolResult{{Tool: "edit_file", Summary: "Edited src/example.cpp"}}

	answer, err := runner.completeWithoutMoreToolsWithReason(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "implement the requested patch"}},
		llama.ChatOptions{},
		results,
		"Tools are now disabled.",
		nil,
	)
	if err != nil {
		t.Fatalf("completeWithoutMoreToolsWithReason: %v", err)
	}
	if requests != 0 {
		t.Fatalf("expected deterministic mutation fallback without model call, got %d request(s)", requests)
	}
	for _, want := range []string{
		"<<QUESTION>>: Implementation incomplete",
		"src/example.cpp",
		"Required edit paths not changed:",
		"src/example.h",
		"pending lane sanity; build/test verification is planner-owned",
	} {
		if !strings.Contains(answer, want) {
			t.Fatalf("expected deterministic mutation answer to include %q, got:\n%s", want, answer)
		}
	}
	if strings.Contains(strings.ToLower(answer), "passed") {
		t.Fatalf("deterministic mutation fallback should not claim unrun verification passed:\n%s", answer)
	}
}

func TestToolStepBudgetPartialRequiredEditsEmitsBlockedCompletion(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "a.txt", "before a\n"); err != nil {
		t.Fatalf("write a: %v", err)
	}
	if err := writeTestFile(root, "b.txt", "before b\n"); err != nil {
		t.Fatalf("write b: %v", err)
	}

	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": `{"tool":"write_file","path":"a.txt","content":"after a\n","description":"partial required edit"}`},
			}},
		})
	}))
	defer server.Close()

	var critic map[string]any
	var completion runnerCompletionStatus
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		Policy: RunPolicy{MaxToolSteps: 1, MaxNoProgressTurns: 4},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:           "implementation",
			Contract:             "implementation_result",
			VerificationContract: "not_required_for_text_mutations",
			PermissionProfile:    "guarded_workspace_write",
			RequiredEditPaths:    []string{"a.txt", "b.txt"},
		},
		OnEvent: func(event RunEvent) {
			switch event.Role {
			case "final_answer_critic":
				if err := json.Unmarshal([]byte(event.Content), &critic); err != nil {
					t.Fatalf("decode critic: %v", err)
				}
			case "completion_status":
				if err := json.Unmarshal([]byte(event.Content), &completion); err != nil {
					t.Fatalf("decode completion: %v", err)
				}
			}
		},
	}
	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Update both required files."}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.HasPrefix(answer, "<<QUESTION>>: Implementation incomplete") || !strings.Contains(answer, "b.txt") {
		t.Fatalf("expected visible incomplete-implementation blocker, got %q", answer)
	}
	if passed, _ := critic["passed"].(bool); passed {
		t.Fatalf("final answer critic should reject partial required edits, got %#v", critic)
	}
	if completion.Status != "blocked" || completion.BlockerKind != "required_edit_paths_remaining" {
		t.Fatalf("expected blocked completion for missing required path, got %#v", completion)
	}
	if requests != 1 {
		t.Fatalf("expected one model tool request before deterministic budget closeout, got %d", requests)
	}
}

func TestModelErrorAfterPartialEditReturnsTypedBlocker(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"a.txt", "b.txt"} {
		if err := writeTestFile(root, path, "before\n"); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{{
					"message": map[string]string{"content": `{"tool":"write_file","path":"a.txt","content":"after\n","description":"partial required edit"}`},
				}},
			})
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	var completion runnerCompletionStatus
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		Policy: RunPolicy{MaxToolSteps: 4, MaxNoProgressTurns: 4},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:           "implementation",
			Contract:             "implementation_result",
			VerificationContract: "not_required_for_text_mutations",
			PermissionProfile:    "guarded_workspace_write",
			RequiredEditPaths:    []string{"a.txt", "b.txt"},
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "completion_status" {
				if err := json.Unmarshal([]byte(event.Content), &completion); err != nil {
					t.Fatalf("decode completion: %v", err)
				}
			}
		},
	}
	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Update both required files."}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run should return typed blocker instead of raw model error: %v", err)
	}
	if !strings.HasPrefix(answer, "<<QUESTION>>: Local model endpoint failed after workspace edits") {
		t.Fatalf("expected model-error typed blocker, got %q", answer)
	}
	for _, want := range []string{"a.txt", "b.txt", "Required edit paths not changed"} {
		if !strings.Contains(answer, want) {
			t.Fatalf("expected blocker to mention %q, got %q", want, answer)
		}
	}
	if completion.Status != "blocked" || completion.BlockerKind != "model_error_after_partial_edit" || !completion.EditAttempted {
		t.Fatalf("unexpected completion status: %#v", completion)
	}
	content, err := os.ReadFile(filepath.Join(root, "a.txt"))
	if err != nil {
		t.Fatalf("read a: %v", err)
	}
	if string(content) != "after\n" {
		t.Fatalf("a.txt content = %q", string(content))
	}
	if requests != 2 {
		t.Fatalf("expected write request then EOF model request, got %d requests", requests)
	}
}

// TestRequiredEditPathReadinessAllowsNoGapInScopeRead was renamed
// from RejectsNoGapReadWithoutTargetPacket after harness fix #4
// (commit 6fe6bce) dropped the named_gap requirement for in-scope
// reads. The new contract: a read targeting a required_edit_path
// is always allowed regardless of named_gap, because the safety
// reason ("verify the anchor on a path we're about to edit") is
// implicit. Out-of-scope reads still get blocked — that's the
// protection the gate is meant to provide. This test now confirms
// the in-scope no-gap read succeeds rather than triggers recovery.
func TestRequiredEditPathReadinessAllowsNoGapInScopeRead(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "a.txt", "seed\n"); err != nil {
		t.Fatalf("write a.txt: %v", err)
	}

	replies := []string{
		`{"tool":"read_file","path":"a.txt","start_line":1,"end_line":1}`,
		`{"tool":"write_file","path":"a.txt","content":"changed\n","description":"make required edit"}`,
		"Changed paths:\n- a.txt\nVerification: not required for this test fixture.",
	}

	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request")
		}
		reply := replies[requests]
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))
	defer server.Close()

	var recoveryPrompt string
	var successfulReads int
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:                  root,
			CWD:                   root,
			EnableWrites:          true,
			CommandTimeoutSeconds: 5,
		}),
		Policy: RunPolicy{MaxToolSteps: 12, MaxNoProgressTurns: 8},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:           "implementation",
			Contract:             "implementation_result",
			VerificationContract: "not_required_for_text_mutations",
			PermissionProfile:    "guarded_workspace_write",
			RequiredEditPaths:    []string{"a.txt"},
			RequiredEditSnippets: []workerruntime.RequiredEditSnippet{{
				Path:    "a.txt",
				Content: "changed",
			}},
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "implementation_scoped_read_recovery" {
				recoveryPrompt = event.Content
			}
		},
	}
	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Update a.txt."}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			if event.Request.Tool == "read_file" && event.Result.Error == "" {
				successfulReads++
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if recoveryPrompt != "" {
		t.Fatalf("in-scope no-gap read should NOT trigger scoped recovery after fix #4; got: %q", recoveryPrompt)
	}
	if successfulReads != 1 {
		t.Fatalf("in-scope no-gap read should succeed (in-scope reads are always allowed); got %d successful read(s)", successfulReads)
	}
	if !strings.Contains(answer, "a.txt") {
		t.Fatalf("expected final answer to report required path, got %q", answer)
	}
	if requests != len(replies) {
		t.Fatalf("expected %d model requests, got %d", len(replies), requests)
	}
	content, err := os.ReadFile(filepath.Join(root, "a.txt"))
	if err != nil {
		t.Fatalf("read a: %v", err)
	}
	if string(content) != "changed\n" {
		t.Fatalf("a.txt content = %q", string(content))
	}
}

// TestWriteReadyPacketReadWithoutNamedGapAllowsInScopeRead was
// renamed from UsesScopedRecovery after harness fix #4 (commit
// 6fe6bce) dropped the named_gap requirement for in-scope reads.
// The new contract: a read targeting a required_edit_path is
// always allowed regardless of named_gap. This test now confirms
// the recovery does NOT fire for an in-scope no-gap read.
func TestWriteReadyPacketReadWithoutNamedGapAllowsInScopeRead(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "a.txt", "before\n"); err != nil {
		t.Fatalf("write a: %v", err)
	}

	replies := []string{
		`{"tool":"read_file","path":"a.txt","start_line":1,"end_line":1}`,
		`{"tool":"write_file","path":"a.txt","content":"after\n","description":"update from packet"}`,
		"Changed paths:\n- a.txt\nVerification: not required for this test fixture.",
	}

	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request")
		}
		reply := replies[requests]
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))
	defer server.Close()

	var sawRecovery bool
	var successfulReads int
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		Policy: RunPolicy{MaxToolSteps: 8, MaxNoProgressTurns: 6},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:           "implementation",
			Contract:             "implementation_result",
			PermissionProfile:    "guarded_workspace_write",
			VerificationContract: "not_required_for_text_mutations",
			RequiredEditPaths:    []string{"a.txt"},
			TargetPacket:         []string{"prompt_context:verified_current_state"},
			EvidencePaths:        []string{"a.txt"},
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "implementation_scoped_read_recovery" {
				sawRecovery = true
			}
		},
	}
	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Update a.txt from the packet."}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			if event.Request.Tool == "read_file" && event.Result.Error == "" {
				successfulReads++
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if sawRecovery {
		t.Fatalf("in-scope no-gap read should NOT trigger scoped recovery after fix #4")
	}
	if successfulReads != 1 {
		t.Fatalf("in-scope no-gap read should succeed; got %d successful read(s)", successfulReads)
	}
	if !strings.Contains(answer, "a.txt") {
		t.Fatalf("expected final answer to report changed path, got %q", answer)
	}
	content, err := os.ReadFile(filepath.Join(root, "a.txt"))
	if err != nil {
		t.Fatalf("read a: %v", err)
	}
	if string(content) != "after\n" {
		t.Fatalf("a.txt content = %q", string(content))
	}
	if requests != len(replies) {
		t.Fatalf("expected %d model requests, got %d", len(replies), requests)
	}
}

func TestWriteReadyPacketRejectsTaskLedgerDetourBeforeFirstWrite(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "a.txt", "before\n"); err != nil {
		t.Fatalf("write a: %v", err)
	}

	replies := []string{
		`{"tool":"task_ledger","todos":[{"id":"read-targets","content":"Read target files","status":"in_progress"},{"id":"edit-a","content":"Edit a.txt","status":"pending"}]}`,
		`{"tool":"write_file","path":"a.txt","content":"after\n","description":"update from packet"}`,
		"Changed paths:\n- a.txt\nVerification: not required for this test fixture.",
	}

	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request")
		}
		reply := replies[requests]
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))
	defer server.Close()

	var sawRecovery bool
	var sawScopedToolRecovery bool
	var successfulLedgerUpdates int
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		Policy: RunPolicy{MaxToolSteps: 8, MaxNoProgressTurns: 6},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:           "implementation",
			Contract:             "implementation_result",
			PermissionProfile:    "guarded_workspace_write",
			VerificationContract: "not_required_for_text_mutations",
			RequiredEditPaths:    []string{"a.txt"},
			TargetPacket:         []string{"prompt_context:verified_current_state"},
			EvidencePaths:        []string{"a.txt"},
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "implementation_write_only_recovery" {
				sawRecovery = true
			}
			if event.Role == "implementation_scoped_tool_recovery" {
				sawScopedToolRecovery = true
			}
		},
	}
	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Update a.txt from the packet."}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			if event.Request.Tool == "task_ledger" && event.Result.Error == "" {
				successfulLedgerUpdates++
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if sawRecovery {
		t.Fatalf("did not expect write-only recovery after task_ledger detour")
	}
	if !sawScopedToolRecovery {
		t.Fatalf("expected scoped tool recovery after task_ledger detour")
	}
	if successfulLedgerUpdates != 0 {
		t.Fatalf("task_ledger should be rejected before the first required-path write, got %d successful update(s)", successfulLedgerUpdates)
	}
	if !strings.Contains(answer, "a.txt") {
		t.Fatalf("expected final answer to report changed path, got %q", answer)
	}
	content, err := os.ReadFile(filepath.Join(root, "a.txt"))
	if err != nil {
		t.Fatalf("read a: %v", err)
	}
	if string(content) != "after\n" {
		t.Fatalf("a.txt content = %q", string(content))
	}
	if requests != len(replies) {
		t.Fatalf("expected %d model requests, got %d", len(replies), requests)
	}
}

func TestWriteReadyPacketAllowsNamedGapProbeBeforeFirstWrite(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"a.txt", "b.txt"} {
		if err := writeTestFile(root, path, "before\n"); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	replies := []string{
		`{"tool":"read_file","path":"a.txt","start_line":1,"end_line":1,"named_gap":"verify the replacement line in a.txt before editing","why_not_answering":"the edit would be unsafe if this anchor is stale"}`,
		`{"tool":"write_file","path":"a.txt","content":"after\n","description":"update from packet"}`,
		"Changed paths:\n- a.txt\nVerification: not required for this test fixture.",
	}

	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request")
		}
		reply := replies[requests]
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))
	defer server.Close()

	var sawRecovery bool
	var successfulReads int
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		Policy: RunPolicy{MaxToolSteps: 8, MaxNoProgressTurns: 6},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:           "implementation",
			Contract:             "implementation_result",
			PermissionProfile:    "guarded_workspace_write",
			VerificationContract: "not_required_for_text_mutations",
			RequiredEditPaths:    []string{"a.txt"},
			TargetPacket:         []string{"prompt_context:verified_current_state"},
			EvidencePaths:        []string{"a.txt"},
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "implementation_write_only_recovery" {
				sawRecovery = true
			}
		},
	}
	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Update a.txt from the packet."}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			if event.Request.Tool == "read_file" && event.Result.Error == "" {
				successfulReads++
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if sawRecovery {
		t.Fatalf("did not expect write-only recovery after named prewrite probe")
	}
	if successfulReads != 1 {
		t.Fatalf("named prewrite read should execute as bounded source refresh, got %d successful read(s)", successfulReads)
	}
	if !strings.Contains(answer, "a.txt") {
		t.Fatalf("expected final answer to report changed path, got %q", answer)
	}
	if requests != len(replies) {
		t.Fatalf("expected %d model requests, got %d", len(replies), requests)
	}
}

func TestWriteReadyPacketAllowsReadOnCompletedRequiredPathWithGap(t *testing.T) {
	// Multi-file scenario: a.txt then b.txt are required. Model writes a.txt,
	// then reads a.txt (now a completed path) with named_gap to set up the
	// b.txt write. Pre-fix this read was rejected because the path dropped
	// out of remainingRequiredEditPaths after the first write. Post-fix the
	// read scope is the brief's full required_edit_paths set, so reads on
	// completed paths within the working set are allowed (when they include
	// named_gap).
	root := t.TempDir()
	for _, path := range []string{"a.txt", "b.txt"} {
		if err := writeTestFile(root, path, "before\n"); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	replies := []string{
		`{"tool":"write_file","path":"a.txt","content":"after a\n","description":"update first required path"}`,
		`{"tool":"read_file","path":"a.txt","start_line":1,"end_line":1,"named_gap":"verify the line a.txt now exposes so b.txt can mirror its structure","why_not_answering":"b.txt's edit must mirror the freshly-landed a.txt content"}`,
		`{"tool":"write_file","path":"b.txt","content":"after b\n","description":"update second required path"}`,
		"Changed paths:\n- a.txt\n- b.txt\nVerification: not required for this test fixture.",
	}

	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request: requests=%d", requests)
		}
		reply := replies[requests]
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))
	defer server.Close()

	var rejectedReads int
	var successfulReads int
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		Policy: RunPolicy{MaxToolSteps: 12, MaxNoProgressTurns: 8},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:           "implementation",
			Contract:             "implementation_result",
			PermissionProfile:    "guarded_workspace_write",
			VerificationContract: "not_required_for_text_mutations",
			RequiredEditPaths:    []string{"a.txt", "b.txt"},
			TargetPacket:         []string{"prompt_context:verified_current_state"},
			EvidencePaths:        []string{"a.txt", "b.txt"},
		},
	}
	_, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Update a.txt and b.txt."}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			if event.Request.Tool == "read_file" {
				if event.Result.Error == "" {
					successfulReads++
				} else {
					rejectedReads++
				}
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rejectedReads != 0 {
		t.Fatalf("expected the named-gap read on a.txt (a completed required path) to pass, got %d rejected reads", rejectedReads)
	}
	if successfulReads != 1 {
		t.Fatalf("expected one successful read on a.txt after the first write, got %d", successfulReads)
	}
	for path, want := range map[string]string{"a.txt": "after a\n", "b.txt": "after b\n"} {
		got, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if string(got) != want {
			t.Fatalf("%s content = %q, want %q", path, string(got), want)
		}
	}
}

func TestWriteReadyPacketRejectsReadOutsideRequiredPathsAfterFirstWrite(t *testing.T) {
	// The relaxation only widens the read scope to the brief's full
	// required_edit_paths set. Reads on paths outside that set must still be
	// rejected — broad codebase exploration is the actual concern.
	root := t.TempDir()
	for _, path := range []string{"a.txt", "b.txt", "unrelated.txt"} {
		if err := writeTestFile(root, path, "before\n"); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	replies := []string{
		`{"tool":"write_file","path":"a.txt","content":"after a\n","description":"update first required path"}`,
		`{"tool":"read_file","path":"unrelated.txt","start_line":1,"end_line":1,"named_gap":"peek at unrelated.txt","why_not_answering":"curious"}`,
		`{"tool":"write_file","path":"b.txt","content":"after b\n","description":"update second required path"}`,
		"Changed paths:\n- a.txt\n- b.txt\nVerification: not required for this test fixture.",
	}

	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request")
		}
		reply := replies[requests]
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))
	defer server.Close()

	var rejectedReads int
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		Policy: RunPolicy{MaxToolSteps: 12, MaxNoProgressTurns: 8},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:           "implementation",
			Contract:             "implementation_result",
			PermissionProfile:    "guarded_workspace_write",
			VerificationContract: "not_required_for_text_mutations",
			RequiredEditPaths:    []string{"a.txt", "b.txt"},
			TargetPacket:         []string{"prompt_context:verified_current_state"},
			EvidencePaths:        []string{"a.txt", "b.txt"},
		},
	}
	_, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Update a.txt and b.txt."}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			if event.Request.Tool == "read_file" && event.Result.Error != "" {
				rejectedReads++
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rejectedReads == 0 {
		t.Fatalf("expected the read on unrelated.txt (outside required_edit_paths) to be rejected even with named_gap")
	}
}

func TestWriteReadyPacketAllowsReadOnExplicitEvidencePath(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"a.txt", "b.txt", "evidence.txt"} {
		if err := writeTestFile(root, path, "before\n"); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	replies := []string{
		`{"tool":"write_file","path":"a.txt","content":"after a\n","description":"update first required path"}`,
		`{"tool":"read_file","path":"evidence.txt","start_line":1,"end_line":1,"named_gap":"verify the explicitly supplied evidence line before updating b.txt","why_not_answering":"b.txt depends on the supplied read-only evidence"}`,
		`{"tool":"write_file","path":"b.txt","content":"after b\n","description":"update second required path"}`,
		"Changed paths:\n- a.txt\n- b.txt\nVerification: not required for this test fixture.",
	}

	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request")
		}
		reply := replies[requests]
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))
	defer server.Close()

	var rejectedReads int
	var successfulReads int
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		Policy: RunPolicy{MaxToolSteps: 12, MaxNoProgressTurns: 8},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:           "implementation",
			Contract:             "implementation_result",
			PermissionProfile:    "guarded_workspace_write",
			VerificationContract: "not_required_for_text_mutations",
			RequiredEditPaths:    []string{"a.txt", "b.txt"},
			TargetPacket:         []string{"prompt_context:verified_current_state"},
			EvidencePaths:        []string{"evidence.txt"},
		},
	}
	_, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Update a.txt and b.txt using evidence.txt."}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			if event.Request.Tool == "read_file" {
				if event.Result.Error == "" {
					successfulReads++
				} else {
					rejectedReads++
				}
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rejectedReads != 0 {
		t.Fatalf("expected explicit evidence-path read to pass, got %d rejected reads", rejectedReads)
	}
	if successfulReads != 1 {
		t.Fatalf("expected one successful evidence-path read, got %d", successfulReads)
	}
}

func TestRunnerForcesSynthesisOnFirstPostReadinessNoGapFollowup(t *testing.T) {
	root := t.TempDir()
	source := `package agent

type Runner struct{}

func (r *Runner) runChildTask() string {
	child := "child"
	return child
}
`
	if err := writeTestFile(root, "internal/agent/runner_child_tasks.go", source); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	replies := []string{
		`{"tool":"go_read_symbol","path":"internal/agent/runner_child_tasks.go","pattern":"Runner.runChildTask"}`,
		`{"tool":"go_read_symbol","path":"internal/agent/runner_child_tasks.go","pattern":"Runner.runWorkbranchChildTask"}`,
		"`Runner.runChildTask` creates the child runner path in `internal/agent/runner_child_tasks.go` and returns its result.",
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request")
		}
		reply := replies[requests]
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))
	defer server.Close()

	var roles []string
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 3, MaxNoProgressTurns: 2},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "architecture",
			Contract:          "architecture_assessment",
			Objective:         "Explain how `Runner.runChildTask` works.",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"go_read_symbol"},
		},
		OnEvent: func(event RunEvent) {
			roles = append(roles, event.Role)
		},
	}
	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Explain how `Runner.runChildTask` works."}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(answer, "runChildTask") {
		t.Fatalf("expected forced synthesis answer, got %q", answer)
	}
	if requests != len(replies) {
		t.Fatalf("expected first no-gap follow-up to force synthesis immediately, used %d/%d replies", requests, len(replies))
	}
	if slices.Contains(roles, "tool_rejected") {
		t.Fatalf("did not expect a rejected-tool turn before forced synthesis, got roles %v", roles)
	}
}

func TestRunnerRequiresVerificationAfterRepeatedWrites(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "README.md", "one\ntwo\nthree\n"); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	replies := []string{
		`{"tool":"read_file","path":"README.md","start_line":1,"end_line":3}`,
		`{"tool":"edit_file","path":"README.md","start_line":1,"end_line":1,"content":"ONE\n","description":"update first line"}`,
		`{"tool":"edit_file","path":"README.md","start_line":2,"end_line":2,"content":"TWO\n","description":"update second line"}`,
		`{"tool":"read_file","path":"README.md"}`,
		`{"tool":"shell","command":"go test ./...","workdir":"."}`,
		`{"tool":"read_file","path":"README.md"}`,
		"Verified after edits.",
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request")
		}
		reply := replies[requests]
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))
	defer server.Close()

	var sawVerificationGate bool
	var sawPostVerificationRead bool
	var resultSummaries []string
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:                  root,
			CWD:                   root,
			EnableShell:           true,
			EnableWrites:          true,
			CommandTimeoutSeconds: 1,
		}),
		Policy: RunPolicy{MaxToolSteps: 6, MaxNoProgressTurns: 3},
	}
	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "edit two lines and verify"}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			resultSummaries = append(resultSummaries, event.Result.Summary+"|"+event.Result.Error)
			if event.Result.Summary == "Verification required after workspace edits" {
				sawVerificationGate = true
			}
			if event.Request.Tool == "read_file" && strings.HasPrefix(event.Result.Summary, "Read ") {
				sawPostVerificationRead = sawVerificationGate
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !sawVerificationGate {
		t.Fatalf("expected non-verification read to be rejected after repeated writes; results=%v", resultSummaries)
	}
	if !sawPostVerificationRead {
		t.Fatalf("expected same read to be allowed after verification attempt; results=%v", resultSummaries)
	}
	if answer != "Verified after edits." {
		t.Fatalf("unexpected answer: %q", answer)
	}
}

func TestRunnerRejectsPostVerificationDriftInVerificationOnlyLane(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{PermissionProfile: "read_only_verification_shell"},
	}
	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Request:               workspace.ToolRequest{Tool: "read_file", Path: "main.go"},
		VerificationAttempted: true,
	})
	if decision.RejectSummary != "Verification-only contract rejected post-verification drift" {
		t.Fatalf("expected post-verification drift rejection, got %#v", decision)
	}
}

func TestApplyLanePreflightRedirectsVerificationOnlyToBootstrapCommand(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			PermissionProfile: "read_only_verification_shell",
			VerificationPlan:  []string{"go test ./..."},
		},
	}

	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Request: workspace.ToolRequest{Tool: "search_files", Pattern: "*.go"},
		Raw:     `{"tool":"search_files","pattern":"*.go"}`,
	})
	if decision.RejectSummary != "" {
		t.Fatalf("expected redirect instead of rejection, got %#v", decision)
	}
	if decision.Request.Tool != "shell" || decision.Request.Command != "go test ./..." || decision.Request.Workdir != "." {
		t.Fatalf("expected bootstrap verification command, got %#v", decision.Request)
	}
	if !strings.Contains(decision.Raw, `"tool":"shell"`) {
		t.Fatalf("expected raw request to be rewritten for redirected verification command, got %q", decision.Raw)
	}
}

func TestApplyLanePreflightForcesAnswerAfterBlindDebugLogReread(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{TaskFamily: "debug_logs"}
	results := []workspace.ToolResult{
		{
			Tool:    "search_files",
			Summary: "Found 1 matching log-like file(s), newest first.",
			Output:  "f1 logs/conversations/recent.jsonl (modified 2026-04-24T10:00:00-04:00; size 2.1 KB)\n",
		},
		{
			Tool:    "read_file",
			Summary: "Read f1 logs/conversations/recent.jsonl lines 1-80",
			Output:  "{\"type\":\"tool_result\"}\n",
		},
	}

	runner := &Runner{RuntimeBrief: brief}
	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Request: workspace.ToolRequest{Tool: "read_file", ID: "f1", Path: "logs/conversations/recent.jsonl"},
		Raw:     `{"tool":"read_file","id":"f1"}`,
		Results: results,
	})
	if decision.RejectSummary != "Blind log reread rejected" {
		t.Fatalf("expected blind-reread rejection, got %#v", decision)
	}
	if decision.ForceAnswer == nil || decision.ForceAnswer.Reason != "evidence_sufficient_after_blind_reread" {
		t.Fatalf("expected forced-answer handoff after blind reread, got %#v", decision)
	}
	if !strings.Contains(decision.ForceAnswer.Prompt, "Evidence is sufficient") {
		t.Fatalf("expected generic evidence-sufficient forced-answer prompt, got %#v", decision.ForceAnswer)
	}
}

func TestInFlightVerificationGateRespectsSkippedVerificationContract(t *testing.T) {
	state := newRunnerExecutionState(false, 0)
	state.writesSinceVerification = 2
	readReq := workspace.ToolRequest{Tool: "read_file", Path: "README.md"}

	if !state.shouldRequireVerification(readReq, workerruntime.WorkerRuntimeBrief{VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker"}) {
		t.Fatal("expected normal mutation contract to require verification after repeated writes")
	}
	if state.shouldRequireVerification(readReq, workerruntime.WorkerRuntimeBrief{VerificationContract: "not_required_for_text_mutations"}) {
		t.Fatal("text-mutation verification contract should skip in-flight verification gate")
	}
	if state.shouldRequireVerification(workspace.ToolRequest{Tool: "task_ledger"}, workerruntime.WorkerRuntimeBrief{VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker"}) {
		t.Fatal("task_ledger should remain allowed while verification is pending")
	}
	if state.shouldRequireVerification(workspace.ToolRequest{Tool: "cpp_diagnostics"}, workerruntime.WorkerRuntimeBrief{VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker"}) {
		t.Fatal("diagnostics tools should remain allowed as verification requests")
	}
}

func TestInFlightVerificationGateSkipsPlannerOwnedVerification(t *testing.T) {
	state := newRunnerExecutionState(false, 0)
	state.writesSinceVerification = 2
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:           "implementation",
		Contract:             "implementation_result",
		Constraints:          []string{"planner_owned_verification"},
		VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
	}

	if state.shouldRequireVerification(workspace.ToolRequest{Tool: "read_file", Path: "src/main.cpp"}, brief) {
		t.Fatal("planner-owned verification lanes should not block follow-up implementation reads after writes")
	}
}

func TestControlRailsUseRuntimeBriefForImplementation(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.RuntimeBriefForRole("implement", "main", "update README"),
	}
	rails := runner.controlRails(nil)
	if rails.TaskFamily != "implementation" || rails.Contract != "implementation_result" {
		t.Fatalf("unexpected rails: %#v", rails)
	}
	if rails.PermissionProfile != "guarded_workspace_write" || rails.VerificationContract == "not_required_for_read_only_lane" {
		t.Fatalf("expected write + verification rails, got %#v", rails)
	}
}

func TestControlRailsReportRequiredEditPathsAndInstructionAttachment(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			RequiredEditPaths: []string{"native/src/job_queue.cpp"},
		},
		InstructionAttacher: NewInstructionAttacher(t.TempDir()),
	}

	rails := runner.controlRails(nil)
	if !slices.Contains(rails.RequiredEditPaths, "native/src/job_queue.cpp") {
		t.Fatalf("expected required edit paths in control rails, got %#v", rails.RequiredEditPaths)
	}
	if !rails.DynamicInstructions {
		t.Fatalf("expected control rails to report dynamic instruction attachment")
	}
}

func TestControlRailsPreserveReadOnlyDiscoveryForDirectAnswer(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "direct_answer",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"search_files", "search_content", "read_file", "go_workspace_symbol", "go_read_symbol"},
		},
	}
	rails := runner.controlRails(nil)
	if rails.PermissionProfile != "read_only_workspace" {
		t.Fatalf("permission = %q, want read_only_workspace", rails.PermissionProfile)
	}
	if len(rails.AllowedTools) == 0 {
		t.Fatal("expected direct-answer rails to keep discovery tools")
	}
	if len(rails.ReadinessGate) != 0 || len(rails.FinalCritic) != 0 {
		t.Fatalf("expected direct-answer rails to avoid extra readiness/final critic scaffolding, got %#v", rails)
	}
}

func TestControlRailsConversationOnlySuppressesAllowedTools(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "direct_answer",
			Contract:          "direct_answer",
			PermissionProfile: "conversation_only",
			AllowedTools:      []string{"task", "search_files", "read_file"},
			FinalCritic:       []string{"answers_actual_user_request"},
		},
	}
	rails := runner.controlRails(nil)
	if rails.PermissionProfile != "conversation_only" {
		t.Fatalf("permission = %q, want conversation_only", rails.PermissionProfile)
	}
	if len(rails.AllowedTools) != 0 {
		t.Fatalf("expected conversation-only rails to suppress allowed tools, got %#v", rails.AllowedTools)
	}
	if !containsRunnerString(rails.FinalCritic, "answers_actual_user_request") {
		t.Fatalf("expected conversation-only rails to keep explicit final critic, got %#v", rails.FinalCritic)
	}
}

func TestControlRailsBlankPermissionInfersReadOnlyWorkspaceWhenToolsExist(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:   "direct_answer",
			Contract:     "direct_answer",
			AllowedTools: []string{"search_files", "read_file"},
		},
	}
	rails := runner.controlRails(nil)
	if rails.PermissionProfile != "read_only_workspace" {
		t.Fatalf("permission = %q, want read_only_workspace", rails.PermissionProfile)
	}
}

func TestControlRailsKeepStrictCriticForDebugLogs(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "debug_logs",
			Contract:          "grounded_explanation",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"search_files", "search_content", "read_file"},
		},
	}
	rails := runner.controlRails(nil)
	if len(rails.ReadinessGate) == 0 || len(rails.FinalCritic) == 0 {
		t.Fatalf("expected debug_logs rails to keep strict operational guidance, got %#v", rails)
	}
}

func TestControlRailsArchitectureKeepsClosureCritic(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "architecture",
			Contract:          "architecture_assessment",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"task", "search_files", "search_content", "read_file", "go_workspace_symbol"},
			RequiredEvidence:  []string{"inspected_paths", "architecture_boundary_evidence", "confidence"},
		},
	}
	rails := runner.controlRails(nil)
	if len(rails.ReadinessGate) == 0 || len(rails.FinalCritic) == 0 {
		t.Fatalf("expected architecture rails to keep closure guidance, got %#v", rails)
	}
}

func TestExplorationDelegationNudgeUsesReadLoopState(t *testing.T) {
	root := t.TempDir()
	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
	}
	if got := runner.explorationDelegationNudge(1, nil); got != "" {
		t.Fatalf("expected no nudge before threshold, got %q", got)
	}
	evidence := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read README.md", Output: "one"},
		{Tool: "search_files", Summary: "Matched 1 file", Output: "README.md"},
	}
	if got := runner.explorationDelegationNudge(4, evidence); !strings.Contains(got, "you may delegate one bounded read_only task") {
		t.Fatalf("expected delegation nudge at threshold, got %q", got)
	}
	done := []workspace.ToolResult{{Tool: "task", Summary: "Child task completed"}}
	if got := runner.explorationDelegationNudge(6, done); got != "" {
		t.Fatalf("expected no nudge after delegated scout, got %q", got)
	}
	runner.RuntimeBrief = workerruntime.WorkerRuntimeBrief{AllowedTools: []string{"read_file", "search_files", "search_content"}}
	if got := runner.explorationDelegationNudge(6, evidence); got != "" {
		t.Fatalf("expected no nudge when task tool is not available under runtime brief, got %q", got)
	}
	if !isReadClassTool(workspace.ToolRequest{Tool: "discovery_query", Purpose: "impact_analysis"}) {
		t.Fatal("discovery_query should count as read-class discovery")
	}
	for _, tool := range []string{"file_stats", "context_impact", "harness_audit"} {
		if !isReadClassTool(workspace.ToolRequest{Tool: tool}) {
			t.Fatalf("%s should count as read-class inspection", tool)
		}
	}

	runner.RuntimeBrief = workerruntime.WorkerRuntimeBrief{
		Contract:     "direct_answer",
		AllowedTools: []string{"task", "read_file", "search_files", "search_content"},
	}
	directAnswerEvidence := []workspace.ToolResult{
		{Tool: "search_content", Summary: "Found 3 matching line(s)", Output: "README.md:1:subagent"},
		{Tool: "search_files", Summary: "Matched 1 file", Output: "README.md"},
	}
	if got := runner.explorationDelegationNudge(1, directAnswerEvidence); got != "" {
		t.Fatalf("expected no direct-answer nudge before tighter threshold, got %q", got)
	}
	if got := runner.explorationDelegationNudge(2, directAnswerEvidence); !strings.Contains(got, "you may delegate one bounded read_only task") {
		t.Fatalf("expected direct-answer nudge at tighter threshold, got %q", got)
	}

	concreteEvidence := append([]workspace.ToolResult(nil), directAnswerEvidence...)
	concreteEvidence = append(concreteEvidence, workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Read internal/transcript/debuglog_test.go lines 1-80",
		Output:  "package transcript",
	})
	if got := runner.explorationDelegationNudge(3, concreteEvidence); got != "" {
		t.Fatalf("expected concrete file inspection to suppress direct-answer delegation, got %q", got)
	}

	runner.RuntimeBrief = workerruntime.WorkerRuntimeBrief{
		Contract:     "architecture_assessment",
		AllowedTools: []string{"task", "read_file", "search_files", "search_content"},
		SearchPolicy: []string{"prefer_discovery_query_for_architecture_boundary", "trace_boundaries_from_named_components"},
	}
	if got := runner.explorationDelegationNudge(1, evidence); got != "" {
		t.Fatalf("expected architecture lane to avoid early delegation bias, got %q", got)
	}
}

func TestPostChildReadBudgetForDirectAnswerIsZero(t *testing.T) {
	if got := postChildReadBudgetForBrief(workerruntime.WorkerRuntimeBrief{Contract: "direct_answer"}); got != 0 {
		t.Fatalf("direct-answer post-child budget = %d, want 0", got)
	}
	if got := postChildReadBudgetForBrief(workerruntime.WorkerRuntimeBrief{Contract: "grounded_explanation", Constraints: []string{"thoroughness=standard"}}); got != 1 {
		t.Fatalf("standard read-only post-child budget = %d, want 1", got)
	}
	if got := postChildReadBudgetForBrief(workerruntime.WorkerRuntimeBrief{Contract: "grounded_explanation", Constraints: []string{"thoroughness=high"}}); got != 2 {
		t.Fatalf("high read-only post-child budget = %d, want 2", got)
	}
	if got := postChildReadBudgetForBrief(workerruntime.WorkerRuntimeBrief{Contract: "grounded_explanation"}); got != 2 {
		t.Fatalf("unspecified read-only post-child budget = %d, want 2", got)
	}
}

func TestExplorationDelegationNudgeSkipsMutationLanes(t *testing.T) {
	root := t.TempDir()
	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: "explore_then_implement",
			Contract:   "implementation_result",
		},
	}
	evidence := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read docs/frontier-routing-implementation-plan.md", Output: "one"},
		{Tool: "go_workspace_symbol", Summary: "Resolved Go workspace symbols", Output: "two"},
	}
	if got := runner.explorationDelegationNudge(6, evidence); got != "" {
		t.Fatalf("expected no delegation nudge for mutation lane, got %q", got)
	}
}

func TestMutationCompletionGateRequiresWriteAndVerification(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.RuntimeBriefForRole("implement", "main", "update README"),
	}
	if result, rejected := runner.mutationCompletionRejection("Done.", nil); !rejected || result.Summary == "" {
		t.Fatalf("expected no-write mutation rejection, got rejected=%v result=%#v", rejected, result)
	}
	if result, rejected := runner.mutationCompletionRejection("Already done.", nil); !rejected || result.Summary == "" {
		t.Fatalf("expected vague already-done answer rejection, got rejected=%v result=%#v", rejected, result)
	}
	// Source-file write (post-#74 lane-aware gate): verification is
	// required for source paths. Markdown writes are exempt — see
	// TestCompletionGateExemptsNonSourceWritesFromVerification.
	write := workspace.ToolResult{Tool: "edit_file", Summary: "Edited foo.go"}
	if result, rejected := runner.mutationCompletionRejection("Done.", []workspace.ToolResult{write}); !rejected || !strings.Contains(result.Error, "verification") {
		t.Fatalf("expected missing-verification rejection, got rejected=%v result=%#v", rejected, result)
	}
	// Source-file edits need source-aware sanity (source_sanity), not
	// just patch hygiene (`git diff --check`). Post-#74 the gate
	// distinguishes these: patch hygiene alone is insufficient for
	// source paths.
	verify := workspace.ToolResult{Tool: "source_sanity", Summary: "Source sanity passed"}
	if result, rejected := runner.mutationCompletionRejection("Done.", []workspace.ToolResult{write, verify}); rejected {
		t.Fatalf("expected verified mutation to pass, got %#v", result)
	}
	unsupportedPathAnswer := "Changed paths:\n- README.md\n- src/missing.go\n\nVerification: git diff --check passed."
	if result, rejected := runner.mutationCompletionRejection(unsupportedPathAnswer, []workspace.ToolResult{write, verify}); !rejected || !strings.Contains(result.Error, "not backed") {
		t.Fatalf("expected unsupported changed-path claim rejection, got rejected=%v result=%#v", rejected, result)
	}
	unsupportedChangesMadeAnswer := "Changes made:\n\n1. **`README.md`** updated.\n2. **`src/missing.go`** updated.\n\nVerification: git diff --check passed."
	if result, rejected := runner.mutationCompletionRejection(unsupportedChangesMadeAnswer, []workspace.ToolResult{write, verify}); !rejected || !strings.Contains(result.Error, "src/missing.go") {
		t.Fatalf("expected unsupported changes-made claim rejection, got rejected=%v result=%#v", rejected, result)
	}
	// Stale-verification: a NEW source-file write after the verify
	// invalidates it (must re-verify). Use a source file so the gate
	// fires; .md writes are exempt post-#74.
	laterWrite := workspace.ToolResult{Tool: "edit_file", Summary: "Edited bar.go"}
	if result, rejected := runner.mutationCompletionRejection("Done.", []workspace.ToolResult{write, verify, laterWrite}); !rejected || !strings.Contains(result.Error, "latest successful write") {
		t.Fatalf("expected stale-verification mutation rejection, got rejected=%v result=%#v", rejected, result)
	}
	if _, rejected := runner.mutationCompletionRejection("No change needed; already set.", nil); rejected {
		t.Fatal("expected explicit no-op answer to pass mutation gate")
	}
}

func TestRunnerReadinessStateTreatsContextPacketAsWriteReadyEvidence(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"native/tests/test_transport_system.cpp"},
		TargetPacket:      []string{"prompt_context:context_packet", "prompt_context:verified_current_state"},
		EvidencePaths:     []string{"native/src/simulation/transport/route_graph.cpp"},
	}
	readiness := runnerReadinessState("main", brief, nil)
	if readiness == nil {
		t.Fatal("expected readiness state")
	}
	if !readiness.ReadyToWrite || readiness.ReadyToAnswer || readiness.ReadyToVerify {
		t.Fatalf("readiness flags = %#v, want write-ready only", readiness)
	}
	for _, want := range []string{"context_packet", "required_edit_paths", "evidence_paths"} {
		if !slices.Contains(readiness.EvidenceSources, want) {
			t.Fatalf("expected evidence source %q, got %#v", want, readiness.EvidenceSources)
		}
	}
	if !slices.Contains(readiness.ClosedClaims, "context_packet_available") {
		t.Fatalf("expected context packet closed claim, got %#v", readiness.ClosedClaims)
	}
	if !slices.Contains(readiness.OpenObligations, "required_edit_paths_not_changed") {
		t.Fatalf("expected required-path open obligation, got %#v", readiness.OpenObligations)
	}
	for _, want := range []string{"semantic/context/declared project discovery", "named_gap", "<<QUESTION>>"} {
		if !strings.Contains(readiness.FurtherReadsRequire, want) {
			t.Fatalf("expected scope-ready read requirement %q, got %q", want, readiness.FurtherReadsRequire)
		}
	}
}

func TestRunnerReadinessStateDoesNotTreatBarePacketMarkerAsWriteReady(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		TargetPacket:      []string{"prompt_context:current_state"},
	}
	readiness := runnerReadinessState("main", brief, nil)
	if readiness == nil {
		t.Fatal("expected readiness state to report packet presence")
	}
	if readiness.ReadyToWrite {
		t.Fatalf("bare packet marker without target/evidence paths should not be write-ready: %#v", readiness)
	}
}

func TestRunnerReadinessStateRequiresVerificationAfterSourceEdit(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:           "implementation",
		Contract:             "implementation_result",
		PermissionProfile:    "guarded_workspace_write",
		RequiredEditPaths:    []string{"internal/app/prompt_routing.go"},
		VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
	}
	results := []workspace.ToolResult{{
		Tool:    "edit_file",
		Summary: "Edited internal/app/prompt_routing.go",
		Output:  "Updated internal/app/prompt_routing.go",
	}}

	readiness := runnerReadinessState("main", brief, results)
	if readiness == nil {
		t.Fatal("expected readiness state")
	}
	if readiness.ReadyToAnswer || !readiness.ReadyToVerify {
		t.Fatalf("readiness flags = %#v, want verification-ready and not answer-ready", readiness)
	}
	if !slices.Contains(readiness.ClosedClaims, "required_edit_paths_changed") {
		t.Fatalf("expected required edit paths closed, got %#v", readiness.ClosedClaims)
	}
	if !slices.Contains(readiness.OpenObligations, "source_aware_verification_pending") {
		t.Fatalf("expected source-aware verification obligation, got %#v", readiness.OpenObligations)
	}
}

func TestRunnerReadinessStateRequiresPlannerOwnedLaneSanityAfterSourceEdit(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:           "implementation",
		Contract:             "implementation_result",
		PermissionProfile:    "guarded_workspace_write",
		RequiredEditPaths:    []string{"native/src/example.cpp"},
		VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
		Constraints:          []string{"planner_owned_verification"},
	}
	results := []workspace.ToolResult{{
		Tool:    "edit_file",
		Summary: "Edited native/src/example.cpp",
	}}

	readiness := runnerReadinessState("main", brief, results)
	if readiness == nil {
		t.Fatal("expected readiness state")
	}
	if readiness.ReadyToAnswer || readiness.ReadyToVerify {
		t.Fatalf("readiness flags = %#v, want pending planner-owned lane sanity", readiness)
	}
	if !slices.Contains(readiness.OpenObligations, "planner_owned_source_lane_sanity_pending") {
		t.Fatalf("expected planner-owned source lane sanity obligation, got %#v", readiness.OpenObligations)
	}
}

func TestRunnerReadinessStateRequiresPlannerOwnedSourceSanityAfterPatchHygiene(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:           "implementation",
		Contract:             "implementation_result",
		PermissionProfile:    "guarded_workspace_write",
		RequiredEditPaths:    []string{"native/src/example.cpp"},
		VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
		Constraints:          []string{"planner_owned_verification"},
	}
	results := []workspace.ToolResult{
		{Tool: "edit_file", Summary: "Edited native/src/example.cpp"},
		{Tool: "shell", Summary: "Ran shell command", Output: "$ git diff --check -- native/src/example.cpp\n"},
	}

	readiness := runnerReadinessState("main", brief, results)
	if readiness == nil {
		t.Fatal("expected readiness state")
	}
	if readiness.ReadyToAnswer || !readiness.ReadyToVerify {
		t.Fatalf("readiness flags = %#v, want source-sanity-ready and not answer-ready", readiness)
	}
	if !slices.Contains(readiness.OpenObligations, "planner_owned_source_lane_sanity_pending") {
		t.Fatalf("expected planner-owned source lane sanity obligation, got %#v", readiness.OpenObligations)
	}
	if !strings.Contains(readiness.FurtherReadsRequire, "source-sanity") {
		t.Fatalf("expected source-sanity follow-up boundary, got %q", readiness.FurtherReadsRequire)
	}
}

// TestCompletionGateExemptsNonSourceWritesFromVerification asserts the
// lane-aware-gate behavior: a write to a non-source file (.md/.txt/etc.)
// in a doc-deliverable lane (refine drafts/reviews/patches, design
// notes — brief.RequiredEditPaths is all non-source) does NOT trigger
// the post-write verification gate. Implementation lanes that touch a
// non-source file (e.g. README.md edited from an implementation atom)
// still verify — patch hygiene is the available signal there.
func TestCompletionGateExemptsNonSourceWritesFromVerification(t *testing.T) {
	cases := []struct {
		name      string
		writePath string
		required  []string
		want      bool
	}{
		{"doc-lane markdown write skips gate", "Wrote draft.md", []string{"draft.md"}, false},
		{"doc-lane txt write skips gate", "Wrote notes.txt", []string{"notes.txt"}, false},
		{"doc-lane go source write requires verification", "Wrote main.go", []string{"main.go"}, true},
		{"doc-lane yaml write requires verification (no exemption yet)", "Wrote config.yaml", []string{"config.yaml"}, true},
		{"implementation-lane markdown write still verifies", "Wrote README.md", nil, true},
		{"implementation-lane markdown with source required path still verifies", "Wrote README.md", []string{"main.go"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			results := []workspace.ToolResult{
				{Tool: "write_file", Summary: c.writePath},
			}
			brief := workerruntime.WorkerRuntimeBrief{
				VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
				RequiredEditPaths:    c.required,
			}
			got := hasPendingVerificationAfterWrites(results, brief)
			if got != c.want {
				t.Fatalf("hasPendingVerificationAfterWrites for %q (required=%v) = %v, want %v", c.writePath, c.required, got, c.want)
			}
		})
	}
}

func TestDocDeliverableMkdirParentDoesNotRequireVerification(t *testing.T) {
	results := []workspace.ToolResult{
		{Tool: "mkdir", Summary: "Created directory .idiom/refine/example/iter-1"},
		{Tool: "write_file", Summary: "Wrote .idiom/refine/example/iter-1/draft.md"},
	}
	brief := workerruntime.WorkerRuntimeBrief{
		VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
		RequiredEditPaths:    []string{".idiom/refine/example/iter-1/draft.md"},
	}
	if hasPendingVerificationAfterWrites(results, brief) {
		t.Fatalf("doc-deliverable mkdir parent plus markdown write should not require source verification")
	}

	sourceBrief := brief
	sourceBrief.RequiredEditPaths = []string{".idiom/refine/example/iter-1/generated.go"}
	if !hasPendingVerificationAfterWrites(results, sourceBrief) {
		t.Fatalf("mkdir exemption must not apply when the required deliverable is source-like")
	}
}

func TestWritePathIsNonSource(t *testing.T) {
	cases := map[string]bool{
		"draft.md":        true,
		"docs/design.md":  true,
		"NOTES.txt":       true,
		"design.markdown": true,
		"README.rst":      true,
		"main.go":         false,
		"config.yaml":     false,
		"package.json":    false,
		"":                false,
		"weird":           false,
		"path/to/file.MD": true, // case-insensitive
	}
	for path, want := range cases {
		if got := writePathIsNonSource(path); got != want {
			t.Errorf("writePathIsNonSource(%q) = %v, want %v", path, got, want)
		}
	}
}

// TestImplementationPreWriteNonWriteRecoveryAllowsSubmitPlanInPlannerPhase
// asserts the fix for #91: when a plan-execute planner-phase lane has
// a write-ready packet, the pre-write non-write-tool gate must accept
// submit_plan (the planner's valid write surface). Before the fix,
// every comprehensive plan-execute brief was rejected because
// IsWorkspaceWriteTool doesn't recognize submit_plan.
func TestImplementationPreWriteNonWriteRecoveryAllowsSubmitPlanInPlannerPhase(t *testing.T) {
	r := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.go", "b.go"},
			Constraints:       []string{"exact_anchor_implementation"},
			RequiredEditSnippets: []workerruntime.RequiredEditSnippet{
				{Path: "a.go", Content: "func foo() {}"},
			},
		},
	}
	req := workspace.ToolRequest{Tool: "submit_plan"}
	summary, errorText, prompt, reject := r.implementationPreWriteNonWriteRecovery(req, nil)
	if reject {
		t.Fatalf("plan-execute planner phase submit_plan must NOT be rejected by pre-write gate; got summary=%q error=%q prompt=%q", summary, errorText, prompt)
	}

	// Plan-execute planner phases no longer use the generic pre-write
	// write-only gate. Reads are governed by the plan-submission
	// readiness contract, not by implementation-lane write recovery.
	reqRead := workspace.ToolRequest{Tool: "read_file"}
	_, _, _, rejectRead := r.implementationPreWriteNonWriteRecovery(reqRead, nil)
	if rejectRead {
		t.Fatal("plan-execute planner reads should not be rejected by the generic implementation pre-write gate")
	}
}

func TestPlanExecuteExactAnchorPlannerDoesNotPreHideReadTools(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "target.txt", "actual\n"); err != nil {
		t.Fatalf("write target: %v", err)
	}

	replies := []string{
		`{"tool":"read_file","path":"target.txt","named_gap":"confirm exact anchor before submit_plan","why_not_answering":"need current target.txt contents to assemble the plan entry"}`,
		`{"tool":"submit_plan","rationale":"Update target.txt using the confirmed exact anchor.","edits":[{"path":"target.txt","old_text":"actual\n","new_text":"updated\n","purpose":"replace the confirmed target text"}]}`,
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request")
		}
		reply := replies[requests]
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": reply}}},
		})
	}))
	defer server.Close()

	var readSucceeded bool
	var hiddenRead bool
	var readErrors []string
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 4, MaxNoProgressTurns: 4},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			Constraints:       []string{"exact_anchor_implementation"},
			RequiredEditPaths: []string{"target.txt"},
			TargetRefs:        []string{"target.txt"},
		},
	}
	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "plan exact-anchor target edit"}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			if event.Result.Tool != "read_file" {
				return
			}
			if strings.Contains(event.Result.Error, "hidden for the rest of this turn") {
				hiddenRead = true
			}
			if strings.TrimSpace(event.Result.Error) != "" {
				readErrors = append(readErrors, event.Result.Error)
				return
			}
			if strings.Contains(event.Result.Output, "actual") {
				readSucceeded = true
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if hiddenRead {
		t.Fatalf("plan-execute exact-anchor planner read_file was hidden before submit_plan; read errors=%v answer=%q", readErrors, answer)
	}
	if !readSucceeded {
		t.Fatalf("expected plan-execute exact-anchor planner to read target.txt before submit_plan; read errors=%v answer=%q", readErrors, answer)
	}
	if strings.Contains(answer, "Exact-anchor implementation did not complete because") {
		t.Fatalf("plan-execute planner should not return exact-anchor hidden-tool blocker, got %q", answer)
	}
	if requests != len(replies) {
		t.Fatalf("expected read then submit_plan, got %d request(s)", requests)
	}
}

func TestPlanExecuteExactAnchorPreWriteRequiresSnippetCoverageForEveryRequiredPath(t *testing.T) {
	covered := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		Constraints:       []string{"exact_anchor_implementation"},
		RequiredEditPaths: []string{"a.txt", "b.txt"},
		RequiredEditSnippets: []workerruntime.RequiredEditSnippet{
			{Path: "a.txt", Content: "old a"},
			{Path: "b.txt", Content: "old b"},
		},
	}
	if !exactAnchorPreWriteSurfaceActive(covered, nil) {
		t.Fatal("plan-execute exact-anchor packet with snippets for every required path should start on submit_plan-only surface")
	}

	missing := covered
	missing.RequiredEditSnippets = []workerruntime.RequiredEditSnippet{{Path: "a.txt", Content: "old a"}}
	if exactAnchorPreWriteSurfaceActive(missing, nil) {
		t.Fatal("plan-execute exact-anchor packet with an uncovered required path must keep scoped reads visible")
	}
}

func TestRepeatedCompletionGateRejectionsReturnTypedBlocker(t *testing.T) {
	replies := []string{"Done.", "Done.", "Done."}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request")
		}
		reply := replies[requests]
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))
	defer server.Close()

	var completion runnerCompletionStatus
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Policy: RunPolicy{MaxToolSteps: 10, MaxNoProgressTurns: 10},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:           "implementation",
			Contract:             "implementation_result",
			PermissionProfile:    "guarded_workspace_write",
			RequiredEditPaths:    []string{"foo.go"},
			VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
			Constraints:          []string{"planner_owned_verification"},
		},
		SeedResults: []workspace.ToolResult{
			// Source-file write requires source-aware sanity; gate
			// rejects "Done." and forces a typed blocker. Non-source
			// writes (.md/.txt) are exempt by design — see
			// hasPendingVerificationAfterWrites.
			{Tool: "edit_file", Summary: "Edited foo.go"},
		},
		OnEvent: func(event RunEvent) {
			if event.Role != "completion_status" {
				return
			}
			if err := json.Unmarshal([]byte(event.Content), &completion); err != nil {
				t.Fatalf("decode completion status: %v", err)
			}
		},
	}
	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Update foo.go."}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if requests != 2 {
		t.Fatalf("expected deterministic stop on second mutation gate rejection, got %d request(s)", requests)
	}
	for _, want := range []string{"<<QUESTION>>:", "Completion gate rejected", "foo.go", "lane sanity"} {
		if !strings.Contains(answer, want) {
			t.Fatalf("expected blocker answer to contain %q, got:\n%s", want, answer)
		}
	}
	if completion.Status != "blocked" || completion.BlockerKind != "completion_gate_lane_sanity_repeated" || !completion.UserVisibleBlocker {
		t.Fatalf("unexpected completion status: %#v", completion)
	}
	if !completion.EditAttempted || !slices.Contains(completion.ChangedPaths, "foo.go") {
		t.Fatalf("expected completion status to carry edited path, got %#v", completion)
	}
}

func TestPartialRequiredEditsCompletionGateUsesBoundedRecovery(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"a.txt", "b.txt"} {
		if err := writeTestFile(root, path, "before\n"); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	replies := []string{
		`{"tool":"write_file","path":"a.txt","content":"after a\n","description":"partial required edit"}`,
		"Done.",
		"Done again.",
	}
	var requests int
	var recoveryPrompt string
	var secondRequestMaxTokens int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request")
		}
		var body struct {
			MaxTokens int `json:"max_tokens"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if requests == 2 {
			secondRequestMaxTokens = body.MaxTokens
		}
		reply := replies[requests]
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))
	defer server.Close()

	var completion runnerCompletionStatus
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		Policy: RunPolicy{MaxToolSteps: 8, MaxNoProgressTurns: 6},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:           "implementation",
			Contract:             "implementation_result",
			PermissionProfile:    "guarded_workspace_write",
			VerificationContract: "not_required_for_text_mutations",
			RequiredEditPaths:    []string{"a.txt", "b.txt"},
		},
		OnEvent: func(event RunEvent) {
			switch event.Role {
			case "completion_gate_recovery":
				recoveryPrompt = event.Content
			case "completion_status":
				if err := json.Unmarshal([]byte(event.Content), &completion); err != nil {
					t.Fatalf("decode completion status: %v", err)
				}
			}
		},
	}
	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Update both required files."}},
		llama.ChatOptions{MaxTokens: 8192},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if requests != len(replies) {
		t.Fatalf("expected one recovery retry after partial edit, got %d request(s)", requests)
	}
	if !strings.Contains(recoveryPrompt, "b.txt") ||
		!strings.Contains(recoveryPrompt, "semantic/context/declared project discovery") ||
		strings.Contains(recoveryPrompt, "Read/search tools are hidden") {
		t.Fatalf("expected recovery prompt to target remaining required path, got %q", recoveryPrompt)
	}
	if secondRequestMaxTokens != 1024 {
		t.Fatalf("expected completion-gate recovery retry to cap max_tokens at 1024, got %d", secondRequestMaxTokens)
	}
	if completion.BlockerKind != "completion_gate_required_paths_remaining_repeated" {
		t.Fatalf("unexpected completion status: %#v", completion)
	}
	for _, want := range []string{"<<QUESTION>>:", "a.txt", "b.txt", "Required edit paths not changed", "Path-level blockers", "b.txt: no successful write"} {
		if !strings.Contains(answer, want) {
			t.Fatalf("expected blocker answer to contain %q, got:\n%s", want, answer)
		}
	}
}

func TestRequiredPathCompletionRecoveryAcceptsPathSpecificBlocker(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"a.txt", "b.txt"} {
		if err := writeTestFile(root, path, "before\n"); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	pathBlocker := "<<QUESTION>>: Blocker for b.txt: cannot safely edit because the provided anchor is stale."
	replies := []string{
		`{"tool":"write_file","path":"a.txt","content":"after a\n","description":"partial required edit"}`,
		"Done.",
		pathBlocker,
	}
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request")
		}
		reply := replies[requests]
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))
	defer server.Close()

	var completion runnerCompletionStatus
	var repeatedBlockerStatus bool
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		Policy: RunPolicy{MaxToolSteps: 8, MaxNoProgressTurns: 6},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:           "implementation",
			Contract:             "implementation_result",
			PermissionProfile:    "guarded_workspace_write",
			VerificationContract: "not_required_for_text_mutations",
			RequiredEditPaths:    []string{"a.txt", "b.txt"},
		},
		OnEvent: func(event RunEvent) {
			if event.Role != "completion_status" {
				return
			}
			if err := json.Unmarshal([]byte(event.Content), &completion); err != nil {
				t.Fatalf("decode completion status: %v", err)
			}
			if completion.BlockerKind == "completion_gate_required_paths_remaining_repeated" {
				repeatedBlockerStatus = true
			}
		},
	}
	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Update both required files."}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if answer != pathBlocker {
		t.Fatalf("expected path-specific blocker answer to be accepted, got %q", answer)
	}
	if requests != len(replies) {
		t.Fatalf("expected one recovery retry, got %d request(s)", requests)
	}
	if repeatedBlockerStatus {
		t.Fatalf("path-specific blocker should not be replaced by repeated completion-gate blocker")
	}
	if completion.BlockerKind != "required_edit_paths_remaining" || !slices.Contains(completion.ChangedPaths, "a.txt") {
		t.Fatalf("expected accepted blocker status for remaining path, got %#v", completion)
	}
}

func TestDecorateFinalAnswerCanonicalizesMissingRequiredPathSuccessClaim(t *testing.T) {
	runner := &Runner{}
	answer := runner.decorateFinalAnswer(FinalAnswerDecorateInput{
		Answer:     "Implemented the requested changes in a.txt and b.txt.",
		StopReason: "model_final",
		UserFacing: true,
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt", "b.txt"},
		},
		Results: []workspace.ToolResult{
			{Tool: "write_file", Summary: "Wrote a.txt"},
		},
	})
	for _, want := range []string{"<<QUESTION>>:", "b.txt", "Changed path recorded: a.txt"} {
		if !strings.Contains(answer, want) {
			t.Fatalf("expected canonical missing-path blocker to contain %q, got %q", want, answer)
		}
	}
	if strings.Contains(answer, "Implemented the requested changes") {
		t.Fatalf("misleading success claim should be replaced, got %q", answer)
	}
}

func TestDecorateFinalAnswerPreservesConcreteMissingPathBlocker(t *testing.T) {
	runner := &Runner{}
	blocker := "Blocked: b.txt cannot be edited safely because the current anchor is stale."
	answer := runner.decorateFinalAnswer(FinalAnswerDecorateInput{
		Answer:     blocker,
		StopReason: "model_final",
		UserFacing: true,
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt", "b.txt"},
		},
		Results: []workspace.ToolResult{
			{Tool: "write_file", Summary: "Wrote a.txt"},
		},
	})
	if answer != blocker {
		t.Fatalf("expected concrete blocker to be preserved, got %q", answer)
	}
}

func TestMutationFallbackAnswerReportsChangedPathsAndVerification(t *testing.T) {
	brief := workerruntime.RuntimeBriefForRole("implement", "main", "update README")
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read README.md", Output: "hello\n"},
		{Tool: "edit_file", Summary: "Edited README.md"},
		{Tool: "shell", Summary: "Ran shell command", Output: "$ git diff --check -- README.md\n"},
	}

	answer, ok := synthesizeNoToolsFallbackAnswer("main", brief, results)
	if !ok {
		t.Fatal("expected mutation fallback answer")
	}
	if strings.Contains(answer, "Partial findings from this turn") {
		t.Fatalf("mutation fallback should not use read-only partial-findings framing, got:\n%s", answer)
	}
	for _, want := range []string{
		"Changed paths:",
		"- README.md",
		"Verification: git diff --check -- README.md passed",
	} {
		if !strings.Contains(answer, want) {
			t.Fatalf("expected mutation fallback to contain %q, got:\n%s", want, answer)
		}
	}
}

func TestMutationFallbackAnswerDropsStaleHarnessBlockerAfterLaterVerification(t *testing.T) {
	brief := workerruntime.RuntimeBriefForRole("implement", "main", "update README")
	results := []workspace.ToolResult{
		{Tool: "edit_file", Summary: "Edited README.md"},
		{
			Tool:    "read_file",
			Summary: "Verification required after workspace edits",
			Error:   "the workspace has 2 successful write(s) since the last verification.",
		},
		{Tool: "shell", Summary: "Ran shell command", Output: "$ git diff --check -- README.md\n"},
	}

	answer, ok := synthesizeNoToolsFallbackAnswer("main", brief, results)
	if !ok {
		t.Fatal("expected mutation fallback answer")
	}
	if strings.Contains(answer, "Blocker:") ||
		strings.Contains(answer, "Verification required after workspace edits") {
		t.Fatalf("stale harness rejection should not survive later verification, got:\n%s", answer)
	}
	if !strings.Contains(answer, "Verification: git diff --check -- README.md passed") {
		t.Fatalf("expected later successful verification, got:\n%s", answer)
	}
}

func TestPrematureTargetReadRejectionRequiresConcreteNonTargetInspection(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: "explore_then_implement",
		TargetRefs: []string{"docs/frontier-routing-implementation-plan.md"},
	}
	req := workspace.ToolRequest{Tool: "read_file", Path: "docs/frontier-routing-implementation-plan.md"}

	broadEvidence := []workspace.ToolResult{
		{Tool: "go_workspace_symbol", Summary: "Resolved Go workspace symbols with gopls", Output: "internal/app/prompt_routing.go:10 func buildPromptExecutionPlan"},
	}
	if _, _, reject := prematureTargetReadRejection(req, broadEvidence, brief); !reject {
		t.Fatal("expected broad workspace-symbol evidence to be insufficient for a large target-file read")
	}

	concreteEvidence := []workspace.ToolResult{
		{Tool: "go_file_outline", Summary: "Outlined 4 top-level declaration(s) in internal/app/prompt_routing.go.", Output: "package app"},
	}
	if _, _, reject := prematureTargetReadRejection(req, concreteEvidence, brief); reject {
		t.Fatal("expected concrete non-target file inspection to unlock the target-file read")
	}
}

func TestEvidenceFirstTargetReadRedirectStartsBoundedTargetReadAfterConcreteEvidence(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: "explore_then_implement",
		TargetRefs: []string{"docs/frontier-routing-implementation-plan.md"},
	}
	evidence := []workspace.ToolResult{
		{Tool: "go_read_symbol", Summary: "Read Model.buildPromptExecutionPlan in internal/app/prompt_routing.go lines 67-99", Output: "func ..."},
	}

	redirected, ok := evidenceFirstTargetReadRedirect(brief, evidence, workspace.ToolRequest{Tool: "go_workspace_symbol", Pattern: "classifyTurnRoute"})
	if !ok {
		t.Fatal("expected post-evidence source exploration to redirect into the target file")
	}
	if redirected.Tool != "read_file" || redirected.Path != "docs/frontier-routing-implementation-plan.md" || redirected.StartLine != 1 || redirected.EndLine != 220 {
		t.Fatalf("unexpected redirect request: %#v", redirected)
	}

	alreadyRead := append([]workspace.ToolResult(nil), evidence...)
	alreadyRead = append(alreadyRead, workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Read f1 docs/frontier-routing-implementation-plan.md lines 1-220",
		Output:  "doc excerpt",
	})
	if _, ok := evidenceFirstTargetReadRedirect(brief, alreadyRead, workspace.ToolRequest{Tool: "go_workspace_symbol", Pattern: "classifyTurnRoute"}); ok {
		t.Fatal("did not expect redirect after the target file had already been read")
	}
}

func TestEvidenceFirstTargetReadUnlockClearsStaleTargetReadDeadEnd(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: "explore_then_implement",
		TargetRefs: []string{"docs/frontier-routing-implementation-plan.md"},
	}
	evidence := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f11 internal/app/prompt_routing.go lines 1-207", Output: "func ..."},
	}
	if !evidenceFirstTargetReadUnlocked(brief, evidence) {
		t.Fatal("expected concrete non-target evidence to unlock the target-read phase")
	}

	rejectedActions := map[string]int{
		toolActionClass(workspace.ToolRequest{Tool: "read_file", Path: "docs/frontier-routing-implementation-plan.md"}): 1,
		"search_content:internal/app": 1,
	}
	clearEvidenceFirstTargetReadActionClasses(rejectedActions, brief)

	if got := rejectedActions[toolActionClass(workspace.ToolRequest{Tool: "read_file", Path: "docs/frontier-routing-implementation-plan.md"})]; got != 0 {
		t.Fatalf("expected target read dead-end state to clear, got %d", got)
	}
	if got := rejectedActions["search_content:internal/app"]; got != 1 {
		t.Fatalf("expected unrelated rejected action to remain, got %d", got)
	}
}

func TestEvidenceFirstTargetReadRedirectBoundsFollowupTargetWindow(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: "explore_then_implement",
		TargetRefs: []string{"docs/frontier-routing-implementation-plan.md"},
	}
	evidence := []workspace.ToolResult{
		{Tool: "go_read_symbol", Summary: "Read Model.buildPromptExecutionPlan in internal/app/prompt_routing.go lines 67-99", Output: "func ..."},
		{Tool: "read_file", Summary: "Read f1 docs/frontier-routing-implementation-plan.md lines 1-220", Output: "doc excerpt"},
	}

	redirected, ok := evidenceFirstTargetReadRedirect(brief, evidence, workspace.ToolRequest{
		Tool: "read_file",
		Path: "docs/frontier-routing-implementation-plan.md",
	})
	if !ok {
		t.Fatal("expected second open-ended target read to be bounded")
	}
	if redirected.Tool != "read_file" || redirected.Path != "docs/frontier-routing-implementation-plan.md" || redirected.StartLine != 221 || redirected.EndLine != 360 {
		t.Fatalf("unexpected follow-up redirect request: %#v", redirected)
	}
}

func TestEvidenceFirstTargetReadRefreshHasNoWindowBudget(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: "explore_then_implement",
		TargetRefs: []string{"docs/frontier-routing-implementation-plan.md"},
	}
	evidence := []workspace.ToolResult{
		{Tool: "go_read_symbol", Summary: "Read Model.buildPromptExecutionPlan in internal/app/prompt_routing.go lines 67-99", Output: "func ..."},
		{Tool: "read_file", Summary: "Read f1 docs/frontier-routing-implementation-plan.md lines 1-220", Output: "doc excerpt"},
		{Tool: "read_file", Summary: "Read f1 docs/frontier-routing-implementation-plan.md lines 221-360", Output: "doc excerpt 2"},
	}

	redirected, ok := evidenceFirstTargetReadRedirect(brief, evidence, workspace.ToolRequest{
		Tool: "read_file",
		Path: "docs/frontier-routing-implementation-plan.md",
	})
	if !ok {
		t.Fatal("expected third open-ended target read to be redirected to the next narrow window")
	}
	if redirected.StartLine != 361 || redirected.EndLine != 500 {
		t.Fatalf("unexpected follow-up redirect request: %#v", redirected)
	}

	if _, ok := evidenceFirstTargetReadRedirect(brief, evidence, workspace.ToolRequest{
		Tool:      "read_file",
		Path:      "docs/frontier-routing-implementation-plan.md",
		StartLine: 240,
		EndLine:   260,
	}); ok {
		t.Fatal("expected precise target read to remain allowed after prior target windows")
	}
}

func TestEvidenceFirstTargetInspectionEncouragesWritesWithoutReadBudget(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: "explore_then_implement",
		TargetRefs: []string{"docs/frontier-routing-implementation-plan.md"},
	}
	evidence := []workspace.ToolResult{
		{Tool: "go_read_symbol", Summary: "Read Model.buildPromptExecutionPlan in internal/app/prompt_routing.go lines 67-99", Output: "func ..."},
		{Tool: "read_file", Summary: "Read f1 docs/frontier-routing-implementation-plan.md lines 1-220", Output: "doc excerpt"},
		{Tool: "read_file", Summary: "Read f1 docs/frontier-routing-implementation-plan.md lines 220-359", Output: "doc excerpt 2"},
	}

	if !evidenceFirstTargetInspectionActive(brief, evidence) {
		t.Fatal("expected target inspection guidance to activate after target evidence")
	}
	if redirected, ok := evidenceFirstTargetReadRedirect(brief, evidence, workspace.ToolRequest{
		Tool: "read_file",
		Path: "docs/frontier-routing-implementation-plan.md",
	}); !ok || redirected.StartLine != 360 {
		t.Fatalf("expected further target reads to stay available as bounded follow-ups, got %#v ok=%v", redirected, ok)
	}
}

func TestRunnerBriefWithPhaseHintsPromotesTargetInspectionPhase(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: "explore_then_implement",
		TargetRefs: []string{"docs/frontier-routing-implementation-plan.md"},
	}
	results := []workspace.ToolResult{
		{Tool: "go_read_symbol", Summary: "Read Model.buildPromptExecutionPlan in internal/app/prompt_routing.go lines 67-99", Output: "func ..."},
		{Tool: "read_file", Summary: "Read f1 docs/frontier-routing-implementation-plan.md lines 1-220", Output: "doc excerpt"},
		{Tool: "read_file", Summary: "Read f1 docs/frontier-routing-implementation-plan.md lines 220-359", Output: "doc excerpt 2"},
	}

	live := runnerBriefWithPhaseHints(brief, results)
	if got := evidenceFirstMutationPhase(live, results); got != "target_inspection" {
		t.Fatalf("phase = %q, want target_inspection", got)
	}
	required := strings.Join(live.RequiredNext, "\n")
	if !strings.Contains(required, "make the edit in the named target once the change is supported") {
		t.Fatalf("expected target-inspection write guidance, got %#v", live.RequiredNext)
	}
	invalid := strings.Join(live.InvalidActions, "\n")
	if !strings.Contains(invalid, "source discovery outside the named target during target inspection") {
		t.Fatalf("expected target-inspection invalid action guidance, got %#v", live.InvalidActions)
	}
}

func TestRunnerPhaseHintsUseCapabilityLanguageForPromptSurfaces(t *testing.T) {
	debugLogSelection := workspace.ToolResult{
		Tool:   "search_files",
		Output: "f1 logs/conversations/2026-05-13.jsonl\n",
	}
	debugLogRead := workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Read f1 logs/conversations/2026-05-13.jsonl lines 1-80",
		Output:  "decision trace",
	}
	nonTargetEvidence := workspace.ToolResult{
		Tool:    "go_read_symbol",
		Summary: "Read Worker.resolve in internal/app/worker.go lines 10-40",
		Output:  "func resolve() {}",
	}
	targetEvidence := workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Read target.go lines 1-80",
		Output:  "package target",
	}

	cases := []struct {
		name    string
		brief   workerruntime.WorkerRuntimeBrief
		results []workspace.ToolResult
	}{
		{
			name:    "debug log tail",
			brief:   workerruntime.WorkerRuntimeBrief{TaskFamily: "debug_logs"},
			results: []workspace.ToolResult{debugLogSelection},
		},
		{
			name:    "selected log analysis",
			brief:   workerruntime.WorkerRuntimeBrief{TaskFamily: "debug_logs"},
			results: []workspace.ToolResult{debugLogSelection, debugLogRead},
		},
		{
			name:  "source evidence",
			brief: workerruntime.WorkerRuntimeBrief{TaskFamily: "explore_then_implement", TargetRefs: []string{"target.go"}},
		},
		{
			name:    "target entry",
			brief:   workerruntime.WorkerRuntimeBrief{TaskFamily: "explore_then_implement", TargetRefs: []string{"target.go"}},
			results: []workspace.ToolResult{nonTargetEvidence},
		},
		{
			name:    "target inspection",
			brief:   workerruntime.WorkerRuntimeBrief{TaskFamily: "explore_then_implement", TargetRefs: []string{"target.go"}},
			results: []workspace.ToolResult{nonTargetEvidence, targetEvidence},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			live := runnerBriefWithPhaseHints(tc.brief, tc.results)
			promptSurface := strings.Join(append(append(append([]string{}, live.RequiredNext...), live.InvalidActions...), live.SearchPolicy...), "\n")
			for _, forbidden := range []string{
				"read the newest selected log tail " + "now",
				"one targeted " + "search_content",
				"using " + "search_content" + " or a narrow " + "read_file",
				"another blind " + "read_file",
				"open-ended " + "read_file",
			} {
				if strings.Contains(promptSurface, forbidden) {
					t.Fatalf("phase hint prompt surface still contains tool strategy prescription %q:\n%s", forbidden, promptSurface)
				}
			}
			for _, rawTool := range []string{"search_content", "read_file"} {
				if strings.Contains(promptSurface, rawTool) {
					t.Fatalf("phase hint prompt surface should use capability/contract language, found %q in:\n%s", rawTool, promptSurface)
				}
			}
		})
	}
}

func TestRunnerPromptSourceDoesNotContainRemovedToolPrescriptions(t *testing.T) {
	removedPromptSurfacePrescriptions := []string{
		"read the newest selected log tail " + "now",
		"prefer one " + "discovery_query first",
		"otherwise stay inside the selected log with one targeted " + "search_content before answering",
		"continue reading only for a named missing target fact, using " + "search_content or a narrow " + "read_file scoped to the named target",
	}
	rawProfileToolMenuTerms := []string{
		"code_navigate",
		"*_find_symbol",
		"_find_symbol",
		"*_read_symbol",
		"_read_symbol",
		"*_references",
		"_references",
		"read_file",
		"project wrappers",
	}

	checks := []struct {
		path      string
		forbidden []string
	}{
		{path: "runner.go", forbidden: removedPromptSurfacePrescriptions},
		{path: "runner_child_tasks.go", forbidden: removedPromptSurfacePrescriptions},
		{path: filepath.Join("..", "..", "cmd", "idiom", "profiles", "claude-qwen-worker.md"), forbidden: rawProfileToolMenuTerms},
		{path: filepath.Join("..", "..", "prompts", "profiles", "claude-qwen-worker.md"), forbidden: rawProfileToolMenuTerms},
	}

	for _, check := range checks {
		t.Run(check.path, func(t *testing.T) {
			body, err := os.ReadFile(check.path)
			if err != nil {
				t.Fatalf("read %s: %v", check.path, err)
			}
			source := string(body)
			for _, forbidden := range check.forbidden {
				if strings.Contains(source, forbidden) {
					t.Fatalf("%s still contains removed prompt-surface prescription %q", check.path, forbidden)
				}
			}
		})
	}
}

func TestRunnerBriefWithPhaseHintsPromotesRequiredPathWriteReadiness(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"native/src/system.cpp", "native/tests/test_system.cpp"},
		RequiredEditSnippets: []workerruntime.RequiredEditSnippet{{
			Path:    "native/src/system.cpp",
			Content: "anchor",
		}},
		RequiredEvidence:     []string{"required_edit_paths_covered_or_blocked"},
		VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
	}

	live := runnerBriefWithPhaseHints(brief, nil)
	if !containsRunnerString(live.Constraints, "implementation_packet_write_ready") {
		t.Fatalf("expected write-ready constraint, got %#v", live.Constraints)
	}
	required := strings.Join(live.RequiredNext, "\n")
	if !strings.Contains(required, "resolve a required path") ||
		!strings.Contains(required, "semantic/context/declared project discovery") ||
		!strings.Contains(required, "exact path-specific blocker") {
		t.Fatalf("expected scoped required-path guidance, got %#v", live.RequiredNext)
	}
	invalid := strings.Join(live.InvalidActions, "\n")
	if !strings.Contains(invalid, "orientation read/search") || !strings.Contains(invalid, "outside required edit paths") {
		t.Fatalf("expected scoped-read invalid action, got %#v", live.InvalidActions)
	}

	results := []workspace.ToolResult{
		{Tool: "edit_file", Summary: "Edited native/src/system.cpp"},
	}
	live = runnerBriefWithPhaseHints(brief, results)
	if !containsRunnerString(live.Constraints, "implementation_required_paths_remaining") {
		t.Fatalf("expected remaining-path constraint, got %#v", live.Constraints)
	}
	if !strings.Contains(strings.Join(live.RequiredNext, "\n"), "remaining required path") {
		t.Fatalf("expected remaining-path guidance, got %#v", live.RequiredNext)
	}
}

func TestRunnerBriefWithPhaseHintsOmitsWriteReadyConstraintWithoutAnchors(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:           "implementation",
		Contract:             "implementation_result",
		PermissionProfile:    "guarded_workspace_write",
		RequiredEditPaths:    []string{"native/src/system.cpp"},
		RequiredEvidence:     []string{"required_edit_paths_covered_or_blocked"},
		VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
	}

	live := runnerBriefWithPhaseHints(brief, nil)
	if containsRunnerString(live.Constraints, "implementation_packet_write_ready") {
		t.Fatalf("write-ready constraint should be omitted when brief lacks anchors, got %#v", live.Constraints)
	}
	if runtimeBriefDefinesWriteReadyReadContract(live) {
		t.Fatalf("write-ready read contract should be inactive without anchors, brief = %#v", live)
	}
}

func TestBuildRunnerNextActionStateIncludesEvidenceFirstPhaseHints(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: "explore_then_implement",
		TargetRefs: []string{"docs/frontier-routing-implementation-plan.md"},
		Objective:  "Explore the routing flow and update the plan doc",
	}
	results := []workspace.ToolResult{
		{Tool: "go_read_symbol", Summary: "Read Model.buildPromptExecutionPlan in internal/app/prompt_routing.go lines 67-99", Output: "func ..."},
		{Tool: "read_file", Summary: "Read f1 docs/frontier-routing-implementation-plan.md lines 1-220", Output: "doc excerpt"},
		{Tool: "read_file", Summary: "Read f1 docs/frontier-routing-implementation-plan.md lines 220-359", Output: "doc excerpt 2"},
	}
	req := workspace.ToolRequest{Tool: "list_files", Path: "internal/app"}
	result := workspace.ToolResult{
		Tool:    "list_files",
		Summary: "Listed 38 path(s).",
	}

	state := buildRunnerNextActionState("main", 17, "tool_result", req, result, results, false, brief)
	if state.Phase != "target_inspection" {
		t.Fatalf("phase = %q, want target_inspection", state.Phase)
	}
	actions := strings.Join(state.NextValidActions, "\n")
	if !strings.Contains(actions, "make the edit in the named target once the change is supported") {
		t.Fatalf("expected target-inspection edit guidance, got %#v", state.NextValidActions)
	}
	invalid := strings.Join(state.InvalidNextActions, "\n")
	if !strings.Contains(invalid, "source discovery outside the named target during target inspection") {
		t.Fatalf("expected target-inspection invalid guidance, got %#v", state.InvalidNextActions)
	}
}

func TestWorkbranchChildTaskPermissionPolicyDeniesDiffHidingAndReleaseCommands(t *testing.T) {
	policy := workbranchChildTaskPermissionPolicy()
	deniedShell := []string{
		"git add README.md",
		"git commit -m child",
		"git reset --hard",
		"git push origin HEAD",
		"gh pr create",
		"gh pr merge 1",
		"gh release create v1",
		"npm publish",
		"pnpm publish",
		"docker push image",
		"kubectl apply -f deploy.yaml",
	}
	for _, command := range deniedShell {
		if decision := policy.Decide(workspace.ToolRequest{Tool: "shell", Command: command}); decision.Decision != PermissionDeny {
			t.Fatalf("%q decision = %q, want deny", command, decision.Decision)
		}
	}
	if decision := policy.Decide(workspace.ToolRequest{Tool: "task", Prompt: "nested"}); decision.Decision != PermissionDeny {
		t.Fatalf("task decision = %q, want deny", decision.Decision)
	}
	if decision := policy.Decide(workspace.ToolRequest{Tool: "edit_file", Path: "README.md"}); decision.Decision != PermissionAllow {
		t.Fatalf("edit_file decision = %q, want allow", decision.Decision)
	}
	if decision := policy.Decide(workspace.ToolRequest{Tool: "shell", Command: "go test ./..."}); decision.Decision != PermissionAllow {
		t.Fatalf("go test decision = %q, want allow", decision.Decision)
	}
}

func TestRunnerExecutesWorkbranchWriteChildTaskWithoutMutatingParent(t *testing.T) {
	root := t.TempDir()
	runGit(t, root, "init")
	runGit(t, root, "config", "user.email", "test@example.com")
	runGit(t, root, "config", "user.name", "Test User")
	if err := writeTestFile(root, "README.md", "hello\n"); err != nil {
		t.Fatalf("write seed file: %v", err)
	}
	runGit(t, root, "add", "README.md")
	runGit(t, root, "commit", "-m", "init")

	replies := []string{
		"```tool\n{\"tool\":\"task\",\"agent\":\"patch-readme\",\"mode\":\"workbranch_write\",\"prompt\":\"change README greeting\"}\n```",
		"```tool\n{\"tool\":\"task_ledger\",\"todos\":[{\"id\":\"edit\",\"content\":\"Edit README\",\"status\":\"completed\"},{\"id\":\"verify\",\"content\":\"Check diff\",\"status\":\"completed\"}]}\n```",
		"```tool\n{\"tool\":\"read_file\",\"path\":\"README.md\"}\n```",
		"```tool\n{\"tool\":\"edit_file\",\"path\":\"README.md\",\"start_line\":1,\"end_line\":1,\"content\":\"hello from child\\n\",\"description\":\"replace greeting with child-authored version\"}\n```",
		"Changed files:\n- README.md\nVerification: inspected diff\nConfidence: medium",
		"Parent inspected child patch.",
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request")
		}
		reply := replies[requests]
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))
	defer server.Close()

	runner := &Runner{
		Client: llama.NewClient(server.URL, 2*time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 3, MaxNoProgressTurns: 3},
	}
	var childPatch string
	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "delegate isolated write"}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			if event.Result.Tool == "task" {
				childPatch = event.Result.Output
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if answer != "Parent inspected child patch." {
		t.Fatalf("unexpected answer: %q", answer)
	}
	data, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatalf("read parent README: %v", err)
	}
	if string(data) != "hello\n" {
		t.Fatalf("parent workspace was mutated: %q", data)
	}
	if !strings.Contains(childPatch, "README.md") || !strings.Contains(childPatch, "hello from child") {
		t.Fatalf("expected child patch output, got:\n%s", childPatch)
	}
}

func TestRunnerRoutesWorkbranchChildOntoImplementerClient(t *testing.T) {
	root := t.TempDir()
	runGit(t, root, "init")
	runGit(t, root, "config", "user.email", "test@example.com")
	runGit(t, root, "config", "user.name", "Test User")
	if err := writeTestFile(root, "README.md", "hello\n"); err != nil {
		t.Fatalf("write seed file: %v", err)
	}
	runGit(t, root, "add", "README.md")
	runGit(t, root, "commit", "-m", "init")

	// Planner replies: delegate, then final summary after child returns.
	plannerReplies := []string{
		"```tool\n{\"tool\":\"task\",\"agent\":\"patch-readme\",\"mode\":\"workbranch_write\",\"prompt\":\"change greeting\"}\n```",
		"Parent inspected child patch.",
	}
	plannerHits := 0
	plannerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if plannerHits >= len(plannerReplies) {
			t.Fatalf("planner received unexpected extra request")
		}
		reply := plannerReplies[plannerHits]
		plannerHits++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": reply}}},
		})
	}))
	defer plannerServer.Close()

	// Implementer replies: drive the child through ledger, read, edit, final.
	implReplies := []string{
		"```tool\n{\"tool\":\"task_ledger\",\"todos\":[{\"id\":\"edit\",\"content\":\"Edit README\",\"status\":\"completed\"},{\"id\":\"verify\",\"content\":\"Check diff\",\"status\":\"completed\"}]}\n```",
		"```tool\n{\"tool\":\"read_file\",\"path\":\"README.md\"}\n```",
		"```tool\n{\"tool\":\"edit_file\",\"path\":\"README.md\",\"start_line\":1,\"end_line\":1,\"content\":\"hello from child\\n\",\"description\":\"replace greeting\"}\n```",
		"Changed files:\n- README.md\nVerification: inspected diff\nConfidence: medium",
	}
	implHits := 0
	implServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if implHits >= len(implReplies) {
			t.Fatalf("implementer received unexpected extra request")
		}
		reply := implReplies[implHits]
		implHits++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": reply}}},
		})
	}))
	defer implServer.Close()

	runner := &Runner{
		Client:            llama.NewClient(plannerServer.URL, 2*time.Second),
		ImplementerClient: llama.NewClient(implServer.URL, 2*time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 3, MaxNoProgressTurns: 3},
	}
	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "delegate"}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if answer != "Parent inspected child patch." {
		t.Fatalf("unexpected answer: %q", answer)
	}
	if plannerHits != len(plannerReplies) {
		t.Fatalf("planner hit count = %d, want %d", plannerHits, len(plannerReplies))
	}
	if implHits == 0 {
		t.Fatal("expected implementer client to receive child-task requests, got none")
	}
}

func TestRunnerReplaysFirstRepeatedToolRequestThenRejectsExactLoop(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "README.md", "hello\n"); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	replies := []string{
		"```tool\n{\"tool\":\"pwd\"}\n```",
		"```tool\n{\"tool\":\"pwd\"}\n```",
		"```tool\n{\"tool\":\"pwd\"}\n```",
		"Stopped after repeated loops.",
	}
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request")
		}
		reply := replies[requests]
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))
	defer server.Close()

	var replayedSummary string
	var rejectedSummary string
	var budgetEvent string
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  ".",
		}),
		Policy: RunPolicy{
			MaxToolSteps:       0,
			MaxNoProgressTurns: 2,
		},
		OnEvent: func(event RunEvent) {
			switch event.Role {
			case "budget_ledger":
				budgetEvent = event.Content
			}
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "read README.md"}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			if strings.Contains(strings.ToLower(event.Result.Summary), "replayed prior evidence") {
				replayedSummary = event.Result.Summary
			}
			if strings.Contains(event.Result.Summary, "Repeated tool request rejected") {
				rejectedSummary = event.Result.Summary
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// DOOM_LOOP guard (threshold=3) trips on the third identical pwd call,
	// records the rejection, then forces a no-tools closeout so callers do
	// not receive an empty final answer.
	if requests < 3 {
		t.Fatalf("expected at least 3 model requests before doom-loop guard, got %d", requests)
	}
	if replayedSummary == "" {
		t.Fatal("expected second identical pwd to replay prior evidence before the guard trips")
	}
	_ = rejectedSummary
	_ = answer
	var ledger runnerBudgetLedger
	if err := json.Unmarshal([]byte(budgetEvent), &ledger); err != nil {
		t.Fatalf("decode budget ledger %q: %v", budgetEvent, err)
	}
	if ledger.StopReason != "doom_loop_guard" {
		t.Fatalf("expected stop_reason=doom_loop_guard, got %#v", ledger)
	}
	if answer != "Stopped after repeated loops." {
		t.Fatalf("expected forced closeout answer, got %q", answer)
	}
	if requests != 4 {
		t.Fatalf("expected three loop requests plus one forced closeout, got %d", requests)
	}
}

func TestRunnerRepeatedBroadInspectionStillHitsExploreGuard(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "README.md", "hello\n"); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	replies := []string{
		"```tool\n{\"tool\":\"list_files\"}\n```",
		"```tool\n{\"tool\":\"list_files\"}\n```",
		"Stopped after broad-inspection guard.",
	}
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request")
		}
		reply := replies[requests]
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))
	defer server.Close()

	mgr := workspace.NewManager(config.Workspace{Root: root, CWD: root})
	if err := mgr.SetMode(workspace.ModeExplore); err != nil {
		t.Fatalf("set explore mode: %v", err)
	}
	runner := &Runner{
		Client:    llama.NewClient(server.URL, time.Second),
		Workspace: mgr,
		Policy: RunPolicy{
			MaxToolSteps:       0,
			MaxNoProgressTurns: 4,
		},
	}

	var broadGuarded bool
	var replayed bool
	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "explore the repo"}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			if strings.Contains(event.Result.Summary, "Additional broad inspection rejected") {
				broadGuarded = true
			}
			if strings.Contains(strings.ToLower(event.Result.Summary), "replayed prior evidence") {
				replayed = true
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if answer != "Stopped after broad-inspection guard." {
		t.Fatalf("unexpected answer %q", answer)
	}
	if !broadGuarded {
		t.Fatal("expected repeated broad inspection to hit the explore guard")
	}
	if replayed {
		t.Fatal("repeated broad inspection must not bypass explore guard through replay")
	}
}

func TestRunnerEmitsRecoveryNudgeBeforeGivingUpOnRepeatedBroadInspection(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "internal/app/app.go", "package app\n"); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	replies := []string{
		`{"tool":"list_files","path":".","recursive":true}`,
		`{"tool":"list_files","path":".","recursive":true}`,
		`{"tool":"list_files","path":".","recursive":true}`,
		"Answered from prior evidence after nudge.",
	}
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request (%d)", requests)
		}
		reply := replies[requests]
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))
	defer server.Close()

	mgr := workspace.NewManager(config.Workspace{Root: root, CWD: root})
	if err := mgr.SetMode(workspace.ModeExplore); err != nil {
		t.Fatalf("set explore mode: %v", err)
	}
	runner := &Runner{
		Client:    llama.NewClient(server.URL, time.Second),
		Workspace: mgr,
		Policy: RunPolicy{
			MaxToolSteps:       0,
			MaxNoProgressTurns: 6,
		},
	}

	var stopReason string
	runner.OnEvent = func(event RunEvent) {
		if event.Role == "budget_ledger" {
			var ledger runnerBudgetLedger
			if err := json.Unmarshal([]byte(event.Content), &ledger); err == nil {
				stopReason = ledger.StopReason
			}
		}
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "identify small refactor opportunities"}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// DOOM_LOOP guard (threshold=3) catches the third identical list_files
	// call before the rejection_recovery_nudge path is reached, then forces
	// a no-tools closeout.
	if stopReason != "doom_loop_guard" {
		t.Fatalf("expected stop_reason=doom_loop_guard, got %q", stopReason)
	}
	if answer != "Answered from prior evidence after nudge." {
		t.Fatalf("expected forced closeout answer, got %q", answer)
	}
	if requests != 4 {
		t.Fatalf("expected three loop requests plus one forced closeout, got %d", requests)
	}
}

func TestRunnerStopsExploreAfterRepeatedMissingPathProbes(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "README.md", "hello\n"); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	replies := []string{
		"```tool\n{\"tool\":\"list_files\",\"path\":\"pkg\",\"recursive\":false,\"max_depth\":1}\n```",
		"```tool\n{\"tool\":\"list_files\",\"path\":\".github\",\"recursive\":false,\"max_depth\":1}\n```",
		"Use README.md and the bootstrapped evidence; no more path probes are needed.",
	}
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request")
		}
		reply := replies[requests]
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))
	defer server.Close()

	mgr := workspace.NewManager(config.Workspace{Root: root, CWD: root})
	if err := mgr.SetMode(workspace.ModeExplore); err != nil {
		t.Fatalf("set explore mode: %v", err)
	}
	var ledger runnerBudgetLedger
	runner := &Runner{
		Client:    llama.NewClient(server.URL, time.Second),
		Workspace: mgr,
		Policy: RunPolicy{
			MaxToolSteps:       8,
			MaxNoProgressTurns: 8,
		},
		OnEvent: func(event RunEvent) {
			if event.Role != "budget_ledger" {
				return
			}
			if err := json.Unmarshal([]byte(event.Content), &ledger); err != nil {
				t.Fatalf("decode budget ledger %q: %v", event.Content, err)
			}
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "explore the repo from bootstrap evidence"}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if answer != "Use README.md and the bootstrapped evidence; no more path probes are needed." {
		t.Fatalf("unexpected answer %q", answer)
	}
	if requests != 3 {
		t.Fatalf("expected forced completion after two missing path probes, got %d requests", requests)
	}
	if ledger.StopReason != "missing_path_probe_budget_exhausted" || !ledger.BudgetExhausted {
		t.Fatalf("expected missing-path budget ledger, got %#v", ledger)
	}
}

func TestRunnerAllowsMissingPathProbeAfterProgress(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "README.md", "hello\n"); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	replies := []string{
		"```tool\n{\"tool\":\"list_files\",\"path\":\"pkg\",\"recursive\":false,\"max_depth\":1}\n```",
		"```tool\n{\"tool\":\"read_file\",\"path\":\"README.md\",\"start_line\":1,\"end_line\":20}\n```",
		"```tool\n{\"tool\":\"list_files\",\"path\":\".github\",\"recursive\":false,\"max_depth\":1}\n```",
		"Completed without forced missing-path stop.",
	}
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request")
		}
		reply := replies[requests]
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))
	defer server.Close()

	mgr := workspace.NewManager(config.Workspace{Root: root, CWD: root})
	if err := mgr.SetMode(workspace.ModeExplore); err != nil {
		t.Fatalf("set explore mode: %v", err)
	}
	var ledger runnerBudgetLedger
	runner := &Runner{
		Client:    llama.NewClient(server.URL, time.Second),
		Workspace: mgr,
		Policy: RunPolicy{
			MaxToolSteps:       8,
			MaxNoProgressTurns: 8,
		},
		OnEvent: func(event RunEvent) {
			if event.Role != "budget_ledger" {
				return
			}
			if err := json.Unmarshal([]byte(event.Content), &ledger); err != nil {
				t.Fatalf("decode budget ledger %q: %v", event.Content, err)
			}
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "explore the repo from bootstrap evidence"}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if answer != "Completed without forced missing-path stop." {
		t.Fatalf("unexpected answer %q", answer)
	}
	if requests != 4 {
		t.Fatalf("expected all scripted model requests, got %d", requests)
	}
	if ledger.StopReason != "model_final" || ledger.BudgetExhausted {
		t.Fatalf("expected ordinary model final after non-consecutive missing path probes, got %#v", ledger)
	}
}

func TestRunnerUnboundedPolicyLetsCompletionGateDriveDone(t *testing.T) {
	replies := []string{"premature final", "accepted final"}
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request")
		}
		reply := replies[requests]
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))
	defer server.Close()

	rejections := 0
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: t.TempDir(),
			CWD:  ".",
		}),
		Policy: RunPolicy{
			MaxToolSteps:       0,
			MaxNoProgressTurns: 3,
		},
		CompletionGate: func(input CompletionGateInput) CompletionGateResult {
			if rejections == 0 {
				rejections++
				return CompletionGateResult{
					Summary: "done gate rejected",
					Error:   "continue once",
				}
			}
			return CompletionGateResult{Accept: true}
		},
	}

	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "work until done"}}, llama.ChatOptions{}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if answer != "accepted final" {
		t.Fatalf("unexpected answer %q", answer)
	}
	if requests != 2 || rejections != 1 {
		t.Fatalf("expected one gate rejection and two model calls, requests=%d rejections=%d", requests, rejections)
	}
}

func TestRunnerTaskLedgerBlocksFinalUntilRequiredItemsComplete(t *testing.T) {
	replies := []string{
		"```tool\n{\"tool\":\"task_ledger\",\"todos\":[{\"id\":\"inspect\",\"content\":\"Inspect target files\",\"status\":\"completed\"},{\"id\":\"edit\",\"content\":\"Apply refactor\",\"status\":\"in_progress\"},{\"id\":\"verify\",\"content\":\"Run verification\",\"status\":\"pending\"}]}\n```",
		"Done too early.",
		"```tool\n{\"tool\":\"task_ledger\",\"todos\":[{\"id\":\"inspect\",\"content\":\"Inspect target files\",\"status\":\"completed\"},{\"id\":\"edit\",\"content\":\"Apply refactor\",\"status\":\"completed\"},{\"id\":\"verify\",\"content\":\"Run verification\",\"status\":\"completed\"}]}\n```",
		"Done after ledger completion.",
	}
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request")
		}
		reply := replies[requests]
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))
	defer server.Close()

	var gateRejections int
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: t.TempDir(),
			CWD:  ".",
		}),
		Policy: RunPolicy{
			MaxToolSteps:       0,
			MaxNoProgressTurns: 6,
		},
		TaskLedger: TaskLedgerPolicy{Required: true},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "perform a complex refactor"}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			if event.Result.Tool == "completion_gate" && strings.Contains(event.Result.Error, "unfinished required work") {
				gateRejections++
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if answer != "Done after ledger completion." {
		t.Fatalf("unexpected answer %q", answer)
	}
	if requests != len(replies) {
		t.Fatalf("expected %d model requests, got %d", len(replies), requests)
	}
	if gateRejections != 1 {
		t.Fatalf("expected one task-ledger completion rejection, got %d", gateRejections)
	}
}

func TestRunnerTaskLedgerRequiredBeforeFinalForComplexWork(t *testing.T) {
	replies := []string{
		"Done without a ledger.",
		"```tool\n{\"tool\":\"task_ledger\",\"todos\":[{\"id\":\"blocked\",\"content\":\"Apply the requested change\",\"status\":\"blocked\",\"evidence\":\"write approval is required\"}]}\n```",
		"Blocked: write approval is required before I can apply the requested change.",
	}
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request")
		}
		reply := replies[requests]
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))
	defer server.Close()

	var missingLedgerRejections int
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: t.TempDir(),
			CWD:  ".",
		}),
		Policy: RunPolicy{
			MaxToolSteps:       0,
			MaxNoProgressTurns: 6,
		},
		TaskLedger: TaskLedgerPolicy{Required: true},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "perform complex work"}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			if event.Result.Tool == "completion_gate" && strings.Contains(event.Result.Error, "no task_ledger") {
				missingLedgerRejections++
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(answer, "Blocked: write approval") {
		t.Fatalf("unexpected answer %q", answer)
	}
	if missingLedgerRejections != 1 {
		t.Fatalf("expected missing-ledger rejection, got %d", missingLedgerRejections)
	}
}

// TestTaskLedgerCompletionGatePlanningExit locks in the planning-mode
// escape hatch: when the user asked only for a plan, the model
// should be able to submit the ledger and reply with an
// explicit planning-exit phrase without the gate forcing it to
// execute every item. Before this fix the model would get stuck
// in a retry loop (or worse, start executing items it was never
// asked to execute).
func TestTaskLedgerCompletionGatePlanningExit(t *testing.T) {
	state := newTaskLedgerState(TaskLedgerPolicy{Required: true})
	apply := state.apply(workspace.ToolRequest{
		Tool: "task_ledger",
		Todos: []workspace.TaskLedgerItem{
			{ID: "a", Content: "refactor X", Status: "pending"},
			{ID: "b", Content: "add tests for X", Status: "pending"},
		},
	})
	if apply.Error != "" {
		t.Fatalf("ledger apply rejected: %v", apply)
	}

	// Plain final answer with unfinished items → gate rejects.
	if _, rejected := state.completionRejection("here is what I'd change"); !rejected {
		t.Fatalf("gate should reject a final answer with unfinished items and no planning exit")
	}

	// Planning-exit phrase → gate allows the final answer through.
	planningReplies := []string{
		"Plan ready — awaiting approval before I execute the items.",
		"Plan complete. Let me know if you'd like me to proceed.",
		"Planning only — please confirm which items to execute.",
		"Awaiting approval on the above plan.",
	}
	for _, reply := range planningReplies {
		if _, rejected := state.completionRejection(reply); rejected {
			t.Errorf("planning-exit reply should pass the gate, rejected: %q", reply)
		}
	}
}

// TestTaskLedgerCompletionGateStillRejectsVagueReplies guards against
// the planning-exit loophole swallowing genuinely-incomplete replies.
// A reply that doesn't contain any recognized terminal phrase must
// still be rejected when items are pending — otherwise the gate
// loses its value.
func TestTaskLedgerCompletionGateStillRejectsVagueReplies(t *testing.T) {
	state := newTaskLedgerState(TaskLedgerPolicy{Required: true})
	apply := state.apply(workspace.ToolRequest{
		Tool: "task_ledger",
		Todos: []workspace.TaskLedgerItem{
			{ID: "a", Content: "refactor X", Status: "in_progress"},
		},
	})
	if apply.Error != "" {
		t.Fatalf("ledger apply rejected: %v", apply)
	}

	vague := []string{
		"Done.",
		"Here's what I found.",
		"I've looked at the code.",
	}
	for _, reply := range vague {
		if _, rejected := state.completionRejection(reply); !rejected {
			t.Errorf("vague reply should still be rejected: %q", reply)
		}
	}
}

func TestTaskLedgerRejectsInvalidUpdates(t *testing.T) {
	state := newTaskLedgerState(TaskLedgerPolicy{})
	result := state.apply(workspace.ToolRequest{
		Tool: "task_ledger",
		Todos: []workspace.TaskLedgerItem{
			{ID: "one", Content: "First active task", Status: "in_progress"},
			{ID: "two", Content: "Second active task", Status: "in_progress"},
		},
	})
	if result.Error == "" || !strings.Contains(result.Error, "only one") {
		t.Fatalf("expected multiple-active rejection, got %#v", result)
	}

	result = state.apply(workspace.ToolRequest{
		Tool: "task_ledger",
		Todos: []workspace.TaskLedgerItem{
			{ID: "one", Content: "Use a bad status", Status: "started"},
		},
	})
	if result.Error == "" || !strings.Contains(result.Error, "unsupported status") {
		t.Fatalf("expected status rejection, got %#v", result)
	}
}

func TestRunnerCompactsLongTurnHistoryWithTaskLedgerState(t *testing.T) {
	replies := []string{
		"```tool\n{\"tool\":\"task_ledger\",\"todos\":[{\"id\":\"inspect\",\"content\":\"Inspect target files\",\"status\":\"in_progress\"},{\"id\":\"verify\",\"content\":\"Run verification\",\"status\":\"pending\"}]}\n```",
		"```tool\n{\"tool\":\"task_ledger\",\"todos\":[{\"id\":\"inspect\",\"content\":\"Inspect target files\",\"status\":\"completed\"},{\"id\":\"verify\",\"content\":\"Run verification\",\"status\":\"in_progress\"}]}\n```",
		"```tool\n{\"tool\":\"task_ledger\",\"todos\":[{\"id\":\"inspect\",\"content\":\"Inspect target files\",\"status\":\"completed\"},{\"id\":\"verify\",\"content\":\"Run verification\",\"status\":\"completed\"}]}\n```",
		"Done after compaction.",
	}
	var requests int
	var sawCompactedPrompt bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Messages []llama.ChatMessage `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		for _, msg := range payload.Messages {
			if strings.Contains(msg.Content, "[runner context compaction]") &&
				strings.Contains(msg.Content, "Task ledger:") &&
				strings.Contains(msg.Content, "verify: Run verification") {
				sawCompactedPrompt = true
			}
		}
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request")
		}
		reply := replies[requests]
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))
	defer server.Close()

	var sawCompactionEvent bool
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: t.TempDir(),
			CWD:  ".",
		}),
		Policy: RunPolicy{
			MaxToolSteps:       0,
			MaxNoProgressTurns: 6,
			MaxContextMessages: 4,
		},
		TaskLedger: TaskLedgerPolicy{Required: true},
		OnEvent: func(event RunEvent) {
			if event.Role == "context_compaction" {
				sawCompactionEvent = true
				if !strings.Contains(event.Content, "runner_task_state") {
					t.Fatalf("expected runner task state compaction event, got %s", event.Content)
				}
			}
		},
	}

	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "perform complex work"}}, llama.ChatOptions{}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if answer != "Done after compaction." {
		t.Fatalf("unexpected answer %q", answer)
	}
	if !sawCompactedPrompt {
		t.Fatal("expected later model request to include compacted runner state")
	}
	if !sawCompactionEvent {
		t.Fatal("expected context_compaction run event")
	}
}

func TestRunnerEmitsBudgetLedgerForModelFinal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": "Done."},
			}},
			"usage": map[string]int{"prompt_tokens": 11, "completion_tokens": 4, "total_tokens": 15},
		})
	}))
	defer server.Close()

	var budgetEvent string
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: t.TempDir(),
			CWD:  ".",
		}),
		Policy: RunPolicy{MaxToolSteps: 3, MaxNoProgressTurns: 3},
		OnEvent: func(event RunEvent) {
			if event.Role == "budget_ledger" {
				budgetEvent = event.Content
			}
		},
	}

	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "answer"}}, llama.ChatOptions{}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if answer != "Done." {
		t.Fatalf("unexpected answer %q", answer)
	}
	var ledger runnerBudgetLedger
	if err := json.Unmarshal([]byte(budgetEvent), &ledger); err != nil {
		t.Fatalf("decode budget ledger %q: %v", budgetEvent, err)
	}
	if ledger.StopReason != "model_final" || ledger.BudgetExhausted || ledger.UsedModelCalls != 1 {
		t.Fatalf("unexpected budget ledger: %#v", ledger)
	}
	if ledger.PromptTokens != 11 || ledger.CompletionTokens != 4 || ledger.TotalTokens != 15 {
		t.Fatalf("expected token usage in budget ledger, got %#v", ledger)
	}
}

func TestRunnerCountsForcedCompletionUsageInBudgetLedger(t *testing.T) {
	replies := []map[string]any{
		{
			"choices": []map[string]any{{
				"message": map[string]string{"content": "```tool\n{\"tool\":\"pwd\"}\n```"},
			}},
			"usage": map[string]int{"prompt_tokens": 5, "completion_tokens": 2, "total_tokens": 7},
		},
		{
			"choices": []map[string]any{{
				"message": map[string]string{"content": "Forced final."},
			}},
			"usage": map[string]int{"prompt_tokens": 13, "completion_tokens": 3, "total_tokens": 16},
		},
	}
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request")
		}
		reply := replies[requests]
		requests++
		_ = json.NewEncoder(w).Encode(reply)
	}))
	defer server.Close()

	var budgetEvent string
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: t.TempDir(),
			CWD:  ".",
		}),
		Policy: RunPolicy{MaxToolSteps: 1, MaxNoProgressTurns: 3},
		OnEvent: func(event RunEvent) {
			if event.Role == "budget_ledger" {
				budgetEvent = event.Content
			}
		},
	}

	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "pwd then stop"}}, llama.ChatOptions{}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if answer != "Forced final." {
		t.Fatalf("unexpected answer %q", answer)
	}
	var ledger runnerBudgetLedger
	if err := json.Unmarshal([]byte(budgetEvent), &ledger); err != nil {
		t.Fatalf("decode budget ledger %q: %v", budgetEvent, err)
	}
	if ledger.StopReason != "tool_step_budget_exhausted" || !ledger.BudgetExhausted {
		t.Fatalf("unexpected budget state: %#v", ledger)
	}
	if ledger.UsedModelCalls != 2 || ledger.PromptTokens != 18 || ledger.CompletionTokens != 5 || ledger.TotalTokens != 23 {
		t.Fatalf("expected forced completion usage in budget ledger, got %#v", ledger)
	}
}

func TestRunnerModelCallBudgetStopsWithLedger(t *testing.T) {
	replies := []string{
		"```tool\n{\"tool\":\"task_ledger\",\"todos\":[{\"id\":\"work\",\"content\":\"Do work\",\"status\":\"in_progress\"}]}\n```",
		"Budget exhausted with work still in progress.",
	}
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request")
		}
		reply := replies[requests]
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))
	defer server.Close()

	var budgetEvent string
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: t.TempDir(),
			CWD:  ".",
		}),
		Policy: RunPolicy{
			MaxToolSteps:       0,
			MaxNoProgressTurns: 6,
			MaxModelCalls:      1,
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "budget_ledger" {
				budgetEvent = event.Content
			}
		},
	}

	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "work"}}, llama.ChatOptions{}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(answer, "Budget exhausted") {
		t.Fatalf("unexpected answer %q", answer)
	}
	var ledger runnerBudgetLedger
	if err := json.Unmarshal([]byte(budgetEvent), &ledger); err != nil {
		t.Fatalf("decode budget ledger %q: %v", budgetEvent, err)
	}
	if ledger.StopReason != "model_call_budget_exhausted" || !ledger.BudgetExhausted || ledger.MaxModelCalls != 1 {
		t.Fatalf("unexpected budget ledger: %#v", ledger)
	}
}

func TestRunnerPermissionPolicyDeniesToolBeforeExecution(t *testing.T) {
	replies := []string{
		"```tool\n{\"tool\":\"edit_file\",\"path\":\"README.md\",\"old_text\":\"hello\",\"new_text\":\"bye\",\"description\":\"change greeting\"}\n```",
		"Blocked by permission policy.",
	}
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reply := replies[requests]
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))
	defer server.Close()

	var permissionEvent string
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: t.TempDir(),
			CWD:  ".",
		}),
		Policy: RunPolicy{MaxToolSteps: 3, MaxNoProgressTurns: 3},
		PermissionPolicy: ToolPermissionPolicy{
			Default: PermissionAllow,
			Rules: []ToolPermissionRule{{
				Tool:        "edit_file",
				PathPattern: "README.md",
				Decision:    PermissionDeny,
			}},
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "permission_decision" && strings.Contains(event.Content, `"decision":"deny"`) {
				permissionEvent = event.Content
			}
		},
	}

	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "edit readme"}}, llama.ChatOptions{}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(answer, "Blocked by permission") {
		t.Fatalf("unexpected answer %q", answer)
	}
	if permissionEvent == "" {
		t.Fatal("expected deny permission decision event")
	}
}

func TestRunnerCreatesCheckpointBeforeFirstWrite(t *testing.T) {
	root := t.TempDir()
	runGit(t, root, "init")
	runGit(t, root, "config", "user.email", "test@example.com")
	runGit(t, root, "config", "user.name", "Test User")
	if err := writeTestFile(root, "README.md", "hello\n"); err != nil {
		t.Fatalf("write seed file: %v", err)
	}
	runGit(t, root, "add", "README.md")
	runGit(t, root, "commit", "-m", "init")

	replies := []string{
		"```tool\n{\"tool\":\"edit_file\",\"path\":\"README.md\",\"old_text\":\"hello\",\"new_text\":\"bye\",\"description\":\"change greeting\"}\n```",
		"Changed README.",
	}
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reply := replies[requests]
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))
	defer server.Close()

	var checkpointEvent string
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  ".",
		}),
		Policy:           RunPolicy{MaxToolSteps: 3, MaxNoProgressTurns: 3},
		CheckpointPolicy: CheckpointPolicy{Enabled: true},
		OnEvent: func(event RunEvent) {
			if event.Role == "checkpoint" {
				checkpointEvent = event.Content
			}
		},
	}

	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "edit readme"}}, llama.ChatOptions{}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(answer, "Changed README") {
		t.Fatalf("unexpected answer %q", answer)
	}
	var checkpoint GitCheckpoint
	if err := json.Unmarshal([]byte(checkpointEvent), &checkpoint); err != nil {
		t.Fatalf("decode checkpoint %q: %v", checkpointEvent, err)
	}
	if checkpoint.Head == "" || checkpoint.Dirty {
		t.Fatalf("unexpected checkpoint: %#v", checkpoint)
	}
}

func TestToolResultMadeProgressClassifiesUsefulAndStalledResults(t *testing.T) {
	if !toolResultMadeProgress(workspace.ToolRequest{Tool: "read_file"}, workspace.ToolResult{Tool: "read_file", Summary: "Read file"}) {
		t.Fatal("read_file summary should count as progress")
	}
	if !toolResultMadeProgress(workspace.ToolRequest{Tool: "task_ledger"}, workspace.ToolResult{Tool: "task_ledger", Summary: "Task ledger updated", Output: "- [completed] one: done"}) {
		t.Fatal("task_ledger output should count as progress")
	}
	if !toolResultMadeProgress(workspace.ToolRequest{Tool: "go_move_declarations"}, workspace.ToolResult{Tool: "go_move_declarations", Summary: "Moved declarations"}) {
		t.Fatal("successful declaration move should count as progress")
	}
	if !toolResultMadeProgress(workspace.ToolRequest{Tool: "apply_patch"}, workspace.ToolResult{Tool: "apply_patch", Summary: "Applied patch with 1 op(s)"}) {
		t.Fatal("successful patch should count as progress")
	}
	if toolResultMadeProgress(workspace.ToolRequest{Tool: "go_move_declarations"}, workspace.ToolResult{Tool: "go_move_declarations", Summary: "No-op declaration move"}) {
		t.Fatal("no-op declaration move should not count as progress")
	}
	if toolResultMadeProgress(workspace.ToolRequest{Tool: "search_content"}, workspace.ToolResult{Tool: "search_content", Error: "failed"}) {
		t.Fatal("failed tools should not count as progress")
	}
	patchPaths := strings.Join(changedPathsFromToolResult(workspace.ToolResult{
		Tool:   "apply_patch",
		Output: "add docs/example.md (8 bytes)\nupdate internal/app/app.go\nupdate+move old.go -> new.go",
	}), "\n")
	for _, want := range []string{"docs/example.md", "internal/app/app.go", "old.go", "new.go"} {
		if !strings.Contains(patchPaths, want) {
			t.Fatalf("expected patch changed paths to include %q, got %q", want, patchPaths)
		}
	}
}

func TestSynthesizeFallbackAnswerUsesNarrowEvidenceAndFinalBlocker(t *testing.T) {
	answer, ok := synthesizeFallbackAnswer("explore", []workspace.ToolResult{
		{Tool: "list_files", Summary: "listed files", Output: "README.md\ninternal/app/app.go"},
		{Tool: "search_content", Summary: "found route", Output: "internal/app/app.go:42: route"},
		{Tool: "shell", Summary: "bad shell", Error: "not allowed"},
	})
	if !ok {
		t.Fatal("expected fallback answer")
	}
	for _, want := range []string{"Partial findings from this turn", "Verified context:", "found route", "Final blocker:", "bad shell: not allowed", "Recommended next step:"} {
		if !strings.Contains(answer, want) {
			t.Fatalf("expected fallback to include %q, got:\n%s", want, answer)
		}
	}
	if strings.Contains(answer, "listed files") {
		t.Fatalf("broad list_files result should not be preferred as narrowing evidence:\n%s", answer)
	}
}

func TestSynthesizeMutationFallbackAnswerDropsErrorsSupersededByLaterWrite(t *testing.T) {
	answer := synthesizeMutationFallbackAnswer(workerruntime.WorkerRuntimeBrief{
		TaskFamily:           "implementation",
		VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
	}, []workspace.ToolResult{
		{
			Tool:    "edit_file",
			Summary: "Missing write description",
			Error:   "edit_file requires a description field",
		},
		{
			Tool:    "edit_file",
			Summary: "Edited src/example.h",
		},
	})

	if !strings.Contains(answer, "src/example.h") {
		t.Fatalf("expected changed path in fallback, got:\n%s", answer)
	}
	if strings.Contains(answer, "Missing write description") || strings.Contains(answer, "Blocker:") {
		t.Fatalf("expected superseded edit error to be omitted, got:\n%s", answer)
	}
	if !strings.Contains(answer, "workspace writes are still pending verification") {
		t.Fatalf("expected verification blocker to remain, got:\n%s", answer)
	}
}

func TestMutationDeterministicFallbackStartsAfterSuccessfulWrite(t *testing.T) {
	brief := workerruntime.RuntimeBriefForRole("implement", "main", "update README")
	pendingResults := []workspace.ToolResult{
		{Tool: "edit_file", Summary: "Edited README.md"},
	}
	if !shouldPreferDeterministicNoToolsAnswer("main", brief, pendingResults) {
		t.Fatal("mutation fallback should be preferred after a successful write")
	}

	verifiedResults := append(pendingResults, workspace.ToolResult{
		Tool:    "shell",
		Summary: "Ran shell command",
		Output:  "$ git diff --check -- README.md\n",
	})
	if !shouldPreferDeterministicNoToolsAnswer("main", brief, verifiedResults) {
		t.Fatal("mutation fallback should be preferred after changed paths and successful verification")
	}
}

func TestMutationDeterministicFallbackStartsForWriteReadyRequiredPaths(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:           "implementation",
		Contract:             "implementation_result",
		PermissionProfile:    "guarded_workspace_write",
		VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
		RequiredEditPaths:    []string{"native/tests/test_energy.cpp"},
	}
	results := []workspace.ToolResult{{Tool: "read_file", Summary: "Read native/src/energy.h"}}
	if !shouldPreferDeterministicNoToolsAnswer("main", brief, results) {
		t.Fatal("write-ready mutation fallback should be preferred once tools are disabled, even before a successful write")
	}
	answer, ok := deterministicNoToolsAnswer("main", brief, results)
	if !ok {
		t.Fatal("expected deterministic no-tools answer")
	}
	for _, want := range []string{"<<QUESTION>>: Implementation incomplete", "Changed paths: none", "native/tests/test_energy.cpp"} {
		if !strings.Contains(answer, want) {
			t.Fatalf("expected deterministic blocker to contain %q, got:\n%s", want, answer)
		}
	}
}

func TestSynthesizeMutationFallbackReportsMissingRequiredPathsAndPlannerOwnedVerification(t *testing.T) {
	answer := synthesizeMutationFallbackAnswer(workerruntime.WorkerRuntimeBrief{
		TaskFamily:           "implementation",
		VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
		Constraints:          []string{"planner_owned_verification"},
		RequiredEditPaths:    []string{"native/src/example.h", "native/src/example.cpp"},
	}, []workspace.ToolResult{
		{Tool: "edit_file", Summary: "Edited native/src/example.cpp"},
	})

	for _, want := range []string{
		"Changed paths:",
		"native/src/example.cpp",
		"Required edit paths not changed:",
		"native/src/example.h",
		"pending lane sanity; build/test verification is planner-owned",
	} {
		if !strings.Contains(answer, want) {
			t.Fatalf("expected mutation fallback to include %q, got:\n%s", want, answer)
		}
	}
}

func TestSynthesizeMutationFallbackReportsPatchHygieneOnlyForPlannerOwnedSourceEdits(t *testing.T) {
	answer := synthesizeMutationFallbackAnswer(workerruntime.WorkerRuntimeBrief{
		TaskFamily:           "implementation",
		VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
		Constraints:          []string{"planner_owned_verification"},
	}, []workspace.ToolResult{
		{Tool: "edit_file", Summary: "Edited native/src/example.cpp"},
		{Tool: "shell", Summary: "Ran shell command", Output: "$ git diff --check -- native/src/example.cpp\n"},
	})

	for _, want := range []string{
		"Verification: git diff --check -- native/src/example.cpp passed (patch hygiene only); source-aware lane sanity still pending; planner-owned build/test verification not run by lane.",
		"native/src/example.cpp",
	} {
		if !strings.Contains(answer, want) {
			t.Fatalf("expected fallback to include %q, got:\n%s", want, answer)
		}
	}
}

func TestHideExactAnchorReadToolsRequiresWriteFirst(t *testing.T) {
	root := t.TempDir()
	manager := workspace.NewManager(config.Workspace{Root: root, CWD: root})
	hidden := map[string]bool{}
	exactHidden := map[string]bool{}

	hideExactAnchorReadTools(hidden, exactHidden, manager)

	for _, tool := range []string{"read_file", "context_status", "context_query", "shell", "git_diff", "pwd"} {
		if !hidden[tool] || !exactHidden[tool] {
			t.Fatalf("expected exact-anchor pre-write mode to hide %s; hidden=%v exact=%v", tool, hidden[tool], exactHidden[tool])
		}
	}
	for _, tool := range []string{"edit_file", "write_file", "append_file", "apply_patch"} {
		if hidden[tool] {
			t.Fatalf("expected exact-anchor pre-write mode to allow %s", tool)
		}
	}
}

func TestExactAnchorPreWriteVisibleBriefAdvertisesWriteOnlySurface(t *testing.T) {
	brief := runnerVisibleBriefForToolSurface(workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		Constraints:       []string{"exact_anchor_implementation"},
		AllowedTools:      []string{"read_file", "search_content", "edit_file", "apply_patch", "shell", "task_ledger", "harness_feedback"},
		RequiredTools:     []string{"edit_file_or_apply_patch", "verification_shell"},
		RequiredEditPaths: []string{"scripts/ui/depot_panel.gd"},
		TargetRefs:        []string{"scripts/ui/depot_panel.gd"},
	}, nil)

	for _, forbidden := range []string{"read_file", "search_content", "shell", "task_ledger", "harness_feedback"} {
		if containsRunnerString(brief.AllowedTools, forbidden) {
			t.Fatalf("exact-anchor pre-write brief advertised %s in allowed tools: %#v", forbidden, brief.AllowedTools)
		}
		manifest, err := json.Marshal(brief.ToolManifest)
		if err != nil {
			t.Fatalf("marshal manifest: %v", err)
		}
		if strings.Contains(string(manifest), fmt.Sprintf(`"tool":"%s"`, forbidden)) {
			t.Fatalf("exact-anchor pre-write manifest advertised %s: %s", forbidden, manifest)
		}
	}
	for _, allowed := range []string{"edit_file", "apply_patch"} {
		if !containsRunnerString(brief.AllowedTools, allowed) {
			t.Fatalf("exact-anchor pre-write brief should keep %s, got %#v", allowed, brief.AllowedTools)
		}
	}
	if !reflect.DeepEqual(brief.RequiredTools, []string{"edit_file_or_apply_patch"}) {
		t.Fatalf("exact-anchor pre-write required tools = %#v, want edit only", brief.RequiredTools)
	}
	if !containsRunnerString(brief.InvalidActions, "read/search/shell before first exact-anchor write") {
		t.Fatalf("expected explicit invalid pre-write read/search/shell action, got %#v", brief.InvalidActions)
	}
}

func TestWriteReadyPreWriteVisibleBriefAdvertisesScopedSurface(t *testing.T) {
	brief := runnerVisibleBriefForToolSurface(workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		Constraints:       []string{"implementation_packet_write_ready"},
		AllowedTools:      []string{"read_file", "search_content", "code_navigate", "edit_file", "write_file", "apply_patch", "task_ledger", "harness_feedback"},
		RequiredTools:     []string{"edit_file_or_apply_patch", "source_sanity_or_verification_shell"},
		RequiredEditPaths: []string{"native/src/simulation/economy/energy.cpp"},
		TargetPacket:      []string{"verified_current_state"},
		EvidencePaths:     []string{"native/src/simulation/economy/energy.cpp"},
	}, nil)

	for _, forbidden := range []string{"search_content", "task_ledger", "harness_feedback"} {
		if containsRunnerString(brief.AllowedTools, forbidden) {
			t.Fatalf("write-ready pre-write brief advertised broad tool %s in allowed tools: %#v", forbidden, brief.AllowedTools)
		}
	}
	for _, allowed := range []string{"read_file", "code_navigate", "edit_file", "write_file", "apply_patch"} {
		if !containsRunnerString(brief.AllowedTools, allowed) {
			t.Fatalf("write-ready pre-write brief should keep scoped tool %s, got %#v", allowed, brief.AllowedTools)
		}
	}
	if !reflect.DeepEqual(brief.RequiredTools, []string{"edit_file_or_apply_patch", "semantic_discovery_or_narrow_read_for_named_gap"}) {
		t.Fatalf("write-ready pre-write required tools = %#v, want scoped edit/read tools", brief.RequiredTools)
	}
	invalid := strings.Join(brief.InvalidActions, "\n")
	if !strings.Contains(invalid, "task_ledger or broad discovery") || !strings.Contains(invalid, "outside required edit paths") {
		t.Fatalf("expected explicit invalid broad/out-of-scope actions, got %#v", brief.InvalidActions)
	}
}

func TestControlRailsExactAnchorPreWriteDoesNotAdvertiseReadShell(t *testing.T) {
	root := t.TempDir()
	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			Constraints:       []string{"exact_anchor_implementation"},
			AllowedTools:      []string{"read_file", "search_content", "edit_file", "apply_patch", "shell", "task_ledger"},
			RequiredEditPaths: []string{"scripts/ui/depot_panel.gd"},
		},
	}

	rails := runner.controlRails(nil)
	for _, forbidden := range []string{"read_file", "search_content", "shell", "task_ledger"} {
		if containsRunnerString(rails.AllowedTools, forbidden) {
			t.Fatalf("exact-anchor pre-write control rails advertised %s: %#v", forbidden, rails.AllowedTools)
		}
	}
	for _, allowed := range []string{"edit_file", "apply_patch"} {
		if !containsRunnerString(rails.AllowedTools, allowed) {
			t.Fatalf("exact-anchor pre-write control rails should keep %s, got %#v", allowed, rails.AllowedTools)
		}
	}
	if !reflect.DeepEqual(rails.RequiredTools, []string{"edit_file_or_apply_patch"}) {
		t.Fatalf("exact-anchor pre-write control rails required tools = %#v, want edit only", rails.RequiredTools)
	}
}

func TestWorkspaceWriteResultCompletedRequiresErrorFreeWrite(t *testing.T) {
	req := workspace.ToolRequest{Tool: "edit_file", Path: "README.md"}
	if !workspaceWriteResultCompleted(req, workspace.ToolResult{Tool: "edit_file", Summary: "Edited README.md"}) {
		t.Fatal("successful edit_file result should complete the exact-anchor write phase")
	}
	if !workspaceWriteResultCompleted(req, workspace.ToolResult{Tool: "edit_file", Summary: "No-op edit for README.md"}) {
		t.Fatal("error-free no-op edit should complete the exact-anchor write phase so verification/closeout can proceed")
	}
	if workspaceWriteResultCompleted(req, workspace.ToolResult{Tool: "edit_file", Summary: "Edit request rejected", Error: "old_text did not appear"}) {
		t.Fatal("failed edit_file result must not release exact-anchor read tools")
	}
	if workspaceWriteResultCompleted(workspace.ToolRequest{Tool: "read_file", Path: "README.md"}, workspace.ToolResult{Tool: "read_file", Summary: "Read README.md"}) {
		t.Fatal("read tools must not complete the exact-anchor write phase")
	}
}

func TestExactAnchorWriteFailureKeepsReadToolsHidden(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "target.txt", "actual\n"); err != nil {
		t.Fatalf("write target: %v", err)
	}
	replies := []string{
		`{"tool":"edit_file","path":"target.txt","old_text":"missing\n","new_text":"replacement\n","description":"replace supplied exact anchor"}`,
		`{"tool":"read_file","path":"target.txt","start_line":1,"end_line":1}`,
		"<<QUESTION>>: supplied exact anchor `missing` did not match target.txt; no workspace change was made.",
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reply := replies[len(replies)-1]
		if requests < len(replies) {
			reply = replies[requests]
		}
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": reply}}},
		})
	}))
	defer server.Close()

	mgr := workspace.NewManager(config.Workspace{
		Root:         root,
		CWD:          root,
		EnableWrites: true,
	})
	mgr.MarkPathObserved("target.txt")
	var events []RunEvent
	var results []workspace.ToolResult
	runner := &Runner{
		Client:    llama.NewClient(server.URL, time.Second),
		Workspace: mgr,
		Policy:    RunPolicy{MaxToolSteps: 5, MaxNoProgressTurns: 6},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			Constraints:       []string{"exact_anchor_implementation"},
			RequiredEditPaths: []string{"target.txt"},
			TargetRefs:        []string{"target.txt"},
		},
		OnEvent: func(event RunEvent) {
			events = append(events, event)
		},
	}
	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "apply exact anchor edit"}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			results = append(results, event.Result)
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.TrimSpace(answer) == "" {
		t.Fatal("expected non-empty final answer after failed exact-anchor write")
	}
	for _, event := range events {
		if event.Role == "tool_hidden" && strings.Contains(event.Content, "restored after successful exact-anchor write") {
			t.Fatalf("read tools should not be restored after failed exact-anchor write; events=%v", eventRolesFor(events))
		}
	}
	foundRetainedHidden := false
	foundHiddenRead := false
	for _, event := range events {
		if event.Role == "tool_hidden" && strings.Contains(event.Content, "remain hidden after failed exact-anchor write") {
			foundRetainedHidden = true
		}
	}
	for _, result := range results {
		if result.Tool == "read_file" && result.Summary == "Tool hidden by active contract" {
			foundHiddenRead = true
		}
	}
	if !foundRetainedHidden {
		t.Fatalf("expected explicit telemetry that read tools stayed hidden; events=%v", eventRolesFor(events))
	}
	if !foundHiddenRead {
		t.Fatalf("expected attempted read to remain hidden after failed write; results=%#v", results)
	}
	content, err := os.ReadFile(filepath.Join(root, "target.txt"))
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(content) != "actual\n" {
		t.Fatalf("failed exact-anchor edit should not change file, got %q", content)
	}
}

func TestExactAnchorRepeatedHiddenToolsReturnTypedBlocker(t *testing.T) {
	root := t.TempDir()
	replies := []string{
		`{"tool":"read_file","path":"target.txt"}`,
		`{"tool":"read_file","path":"target.txt"}`,
		`{"tool":"shell","command":"cat target.txt"}`,
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request after deterministic exact-anchor blocker")
		}
		reply := replies[requests]
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": reply}}},
		})
	}))
	defer server.Close()

	var completion runnerCompletionStatus
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 8, MaxNoProgressTurns: 8},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			Constraints:       []string{"exact_anchor_implementation"},
			RequiredEditPaths: []string{"target.txt"},
			TargetRefs:        []string{"target.txt"},
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "completion_status" {
				if err := json.Unmarshal([]byte(event.Content), &completion); err != nil {
					t.Fatalf("decode completion status: %v", err)
				}
			}
		},
	}
	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "apply exact anchor edit"}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.HasPrefix(answer, "<<QUESTION>>:") || !strings.Contains(answer, "No workspace change was made") {
		t.Fatalf("expected deterministic exact-anchor blocker, got %q", answer)
	}
	if completion.Status != "blocked" || completion.BlockerKind != "exact_anchor_prewrite_hidden_tool_repeated" || !completion.RequiresEdit {
		t.Fatalf("unexpected completion status: %#v", completion)
	}
	if requests != len(replies) {
		t.Fatalf("expected deterministic blocker without final-answer model call, got %d request(s)", requests)
	}
}

func TestExactAnchorRejectedWritesAfterRecoveryReturnTypedBlocker(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "target.txt", "actual\n"); err != nil {
		t.Fatalf("write target: %v", err)
	}
	replies := []string{
		`{"tool":"edit_file","path":"target.txt","description":"empty write shape"}`,
		`{"tool":"edit_file","path":"target.txt","old_text":"missing anchor","new_text":"replacement\n","description":"bad anchor"}`,
		`{"tool":"edit_file","path":"target.txt","start_line":1,"end_line":1,"content":"","description":"bad empty line edit"}`,
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request after deterministic exact-anchor write blocker")
		}
		reply := replies[requests]
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": reply}}},
		})
	}))
	defer server.Close()

	var completion runnerCompletionStatus
	mgr := workspace.NewManager(config.Workspace{
		Root:         root,
		CWD:          root,
		EnableWrites: true,
	})
	mgr.MarkPathObserved("target.txt")
	runner := &Runner{
		Client:    llama.NewClient(server.URL, time.Second),
		Workspace: mgr,
		Policy:    RunPolicy{MaxToolSteps: 8, MaxNoProgressTurns: 8},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			Constraints:       []string{"exact_anchor_implementation"},
			RequiredEditPaths: []string{"target.txt"},
			TargetRefs:        []string{"target.txt"},
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "completion_status" {
				if err := json.Unmarshal([]byte(event.Content), &completion); err != nil {
					t.Fatalf("decode completion status: %v", err)
				}
			}
		},
	}
	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "apply exact anchor edit"}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.HasPrefix(answer, "<<QUESTION>>:") ||
		!strings.Contains(answer, "repeated edit_file write attempts") ||
		!strings.Contains(answer, "empty or whitespace-only content") {
		t.Fatalf("expected deterministic repeated-write blocker, got %q", answer)
	}
	if completion.Status != "blocked" || completion.BlockerKind != "exact_anchor_write_failure_repeated" || !completion.RequiresEdit {
		t.Fatalf("unexpected completion status: %#v", completion)
	}
	if requests != len(replies) {
		t.Fatalf("expected deterministic blocker without extra model call, got %d request(s)", requests)
	}
	content, err := os.ReadFile(filepath.Join(root, "target.txt"))
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(content) != "actual\n" {
		t.Fatalf("rejected exact-anchor writes should not change file, got %q", content)
	}
}

func TestSynthesizeFallbackAnswerPrefersSemanticEvidenceOverRedirects(t *testing.T) {
	answer, ok := synthesizeFallbackAnswer("child:explorer", []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Redirected internal/workspace/tool_schemas.go to go_file_outline",
			Output:  "candidate_request: {\"tool\":\"go_file_outline\",\"path\":\"internal/workspace/tool_schemas.go\"}\nrelated_capability: read_symbol via go_read_symbol.",
		},
		{
			Tool:    "go_file_outline",
			Summary: "Outlined 7 top-level declaration(s) in internal/workspace/tool_schemas.go.",
			Output:  "package workspace\ndeclarations:\n- baseToolDefinitions lines 69-743",
		},
		{
			Tool:    "go_read_symbol",
			Summary: "Read baseToolDefinitions in internal/workspace/tool_schemas.go lines 69-89",
			Output:  "69:func baseToolDefinitions() []ToolDefinition {\n80:\t\t\tName:        \"task\"",
		},
	})
	if !ok {
		t.Fatal("expected fallback answer")
	}
	if strings.Contains(answer, "Redirected to go_file_outline") || strings.Contains(answer, "Try: {\"tool\"") {
		t.Fatalf("expected redirect chatter to be omitted from fallback packet, got:\n%s", answer)
	}
	for _, want := range []string{"internal/workspace/tool_schemas.go", "baseToolDefinitions", "Name:        \"task\""} {
		if !strings.Contains(answer, want) {
			t.Fatalf("expected semantic evidence %q in fallback packet, got:\n%s", want, answer)
		}
	}
}

// Adoption fix B (OpenCode alignment) — the forced-completion fallback must
// NOT prefix the reply with apologetic "did not complete cleanly" wording.
// Users read that as a system error; the same content reframed as
// "Partial findings from this turn" presents the evidence honestly.
// Guards against the failure surfaced in
// logs/conversations/20260421-053044.479.jsonl where the fallback answer
// was an apology instead of a useful partial report.
func TestSynthesizeFallbackAnswerOmitsApologeticPrefix(t *testing.T) {
	for _, agent := range []string{"explore", "main"} {
		answer, ok := synthesizeFallbackAnswer(agent, []workspace.ToolResult{
			{Tool: "read_file", Summary: "Read internal/app/app.go lines 1-50", Output: "package app\n"},
		})
		if !ok {
			t.Fatalf("expected fallback answer for agent %q", agent)
		}
		if !strings.Contains(answer, "Partial findings from this turn") {
			t.Fatalf("expected %q fallback to lead with partial-findings framing, got:\n%s", agent, answer)
		}
		if strings.Contains(answer, "did not complete cleanly") {
			t.Fatalf("expected %q fallback to drop apologetic wording, got:\n%s", agent, answer)
		}
	}
}

func TestImmediateBudgetFallbackAnswerUsesExistingEvidence(t *testing.T) {
	answer, ok := immediateBudgetFallbackAnswer("main", workerruntime.WorkerRuntimeBrief{
		Contract:          "direct_answer",
		PermissionProfile: "read_only_workspace",
		Objective:         "How does this harness utilize sub-agents?",
	}, "Post-child named-gap contract is exhausted. Tools are now disabled.", []workspace.ToolResult{
		{
			Tool:    "task",
			Summary: "Child task completed",
			Output:  "Question: How does this harness utilize sub-agents?\nSource: delegated read-only evidence packet.\n\nInspected paths:\n- `internal/agent/runner.go`\n- `internal/agent/runner_child_tasks.go`\n\nKey findings:\n- The harness uses the `task` tool to launch a read-only child runner.\n",
		},
		{Tool: "read_file", Summary: "Read internal/agent/runner.go lines 1-20", Output: "func (r *Runner) runChildTask(...) {}"},
	})
	if !ok {
		t.Fatal("expected immediate fallback answer")
	}
	for _, want := range []string{"Source: delegated read-only evidence packet.", "`internal/agent/runner.go`", "`internal/agent/runner_child_tasks.go`"} {
		if !strings.Contains(answer, want) {
			t.Fatalf("expected fallback to include %q, got:\n%s", want, answer)
		}
	}
	if strings.Contains(answer, "Best grounded answer from the gathered evidence") {
		t.Fatalf("expected evidence answer without fallback banner, got:\n%s", answer)
	}
}

func TestAnnotateScopedFallbackAnswerAddsObjectiveForChildPacketFallback(t *testing.T) {
	answer := annotateScopedFallbackAnswer("Partial findings from this turn (the budget was reached before a full synthesis):\n\nVerified context:\n- Found task hooks", "Can you tell me how this harness uses subagents?")
	if !strings.Contains(answer, "Question in scope: Can you tell me how this harness uses subagents?") {
		t.Fatalf("expected scoped question prefix, got:\n%s", answer)
	}
	if !strings.Contains(answer, "Source: delegated read-only evidence packet.") {
		t.Fatalf("expected delegated read-only packet source line, got:\n%s", answer)
	}
}

func TestChildPacketContainsGroundedAnswerEvidenceRequiresRealEvidence(t *testing.T) {
	good := "Inspected paths: internal/agent/runner.go, internal/agent/runner_child_tasks.go\nKey findings: the harness uses the task tool to launch an isolated read-only child runner.\nConfidence: high\n"
	if !childPacketContainsGroundedAnswerEvidence("", good) {
		t.Fatal("expected grounded child packet evidence to be recognized")
	}
	partial := "Partial findings from this turn (the budget was reached before a full synthesis):\n\nVerified context:\n- Read internal/agent/runner.go lines 100-140\n- Found internal/app/turn_planner.go references\n"
	if !childPacketContainsGroundedAnswerEvidence("", partial) {
		t.Fatal("expected partial packet with grounded file evidence to be recognized")
	}
	bad := "Partial findings from this turn (the budget was reached before a full synthesis):\n\nVerified context:\n- Redirected to go_file_outline\ncandidate_request: {\"tool\":\"go_file_outline\",\"path\":\"internal/workspace/tool_schemas.go\"}\nrelated_capability: read_symbol via go_read_symbol.\n"
	if childPacketContainsGroundedAnswerEvidence("", bad) {
		t.Fatal("expected redirect packet without grounded evidence to be rejected")
	}
	fixtureOnly := "Inspected paths: testdata/swe/fixtures/prompt-injection-repo/README.md, codex/README.md\nKey findings: child packet ready."
	if childPacketContainsGroundedAnswerEvidence("", fixtureOnly) {
		t.Fatal("expected fixture/docs-only child packet to be rejected for non-doc objectives")
	}
	docsOnly := "Inspected paths: README.md\nKey findings: documentation entrypoint found."
	if !childPacketContainsGroundedAnswerEvidence("Update the README documentation.", docsOnly) {
		t.Fatal("expected docs objective to accept documentation-only child packet")
	}
}

func TestNormalizeDelegatedReadOnlyPacketStripsTransientHandles(t *testing.T) {
	packet := "Partial findings from this turn (the budget was reached before a full synthesis):\n\nVerified context:\n- Read f23 internal/agent/runner_child_tasks.go lines 1-40\n- The caller is internal/agent/runner.go:574 and the composer lives in internal/agent/answer_composer.go``\nf23 internal/agent/runner_child_tasks.go\nf7 README.md\n"
	got := normalizeDelegatedReadOnlyPacket(packet)
	if strings.Contains(got, "f23 ") || strings.Contains(got, "f7 ") {
		t.Fatalf("expected transient handles to be stripped, got:\n%s", got)
	}
	if strings.Contains(got, "internal/agent/runner.go:574") || strings.Contains(got, "internal/agent/answer_composer.go``") {
		t.Fatalf("expected packet normalization to strip speculative line citations and malformed quotes, got:\n%s", got)
	}
	for _, want := range []string{"internal/agent/runner_child_tasks.go", "internal/agent/runner.go", "internal/agent/answer_composer.go", "README.md"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected normalized packet to retain %q, got:\n%s", want, got)
		}
	}
}

func TestUniqueDelegatedDiscoveryPathsNormalizesPacketArtifacts(t *testing.T) {
	paths := uniqueDelegatedDiscoveryPaths([]string{
		"internal/agent/runner.go``",
		"internal/agent/runner.go:648",
		"internal/agent/runner_child_tasks.go:93-94",
		"`internal/agent/answer_composer.go`",
	})
	want := []string{
		"internal/agent/runner.go",
		"internal/agent/runner_child_tasks.go",
		"internal/agent/answer_composer.go",
	}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("expected normalized delegated paths %#v, got %#v", want, paths)
	}
}

func TestDelegatedReadOnlyBootstrapRedirectsBroadInitialSearch(t *testing.T) {
	runner := &Runner{
		AgentName: "child:explorer",
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:   "delegated_read_only",
			AllowedTools: []string{"discovery_query", "search_content", "read_file"},
			Objective:    "Explain how the harness uses sub-agents.\nNamed workspace scope: internal, cmd, prompts\nKey terms: harness, sub-agent, subagent",
		},
	}
	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Request: workspace.ToolRequest{Tool: "search_files", Pattern: "*.go"},
	})
	if decision.Request.Tool != "discovery_query" || decision.Request.Purpose != "architecture_boundary" {
		t.Fatalf("expected bootstrap redirect to architecture discovery_query, got %#v", decision.Request)
	}
	if decision.Request.Path != "internal" {
		t.Fatalf("expected bootstrap redirect to scope internal, got %#v", decision.Request)
	}
	for _, want := range []string{"sub[-_ ]?agent", "subagent"} {
		if !strings.Contains(decision.Request.Pattern, want) {
			t.Fatalf("expected bootstrap pattern to include %q, got %q", want, decision.Request.Pattern)
		}
	}
}

func TestDelegatedReadOnlyBootstrapFallsBackToSearchWithoutDiscoveryQuery(t *testing.T) {
	runner := &Runner{
		AgentName: "child:explorer",
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:   "delegated_read_only",
			AllowedTools: []string{"search_content", "read_file"},
			Objective:    "Explain how the harness uses sub-agents.\nNamed workspace scope: internal, cmd, prompts\nKey terms: harness, sub-agent, subagent",
		},
	}
	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Request: workspace.ToolRequest{Tool: "search_files", Pattern: "*.go"},
	})
	if decision.Request.Tool != "search_content" {
		t.Fatalf("expected bootstrap redirect to search_content, got %#v", decision.Request)
	}
}

func TestDelegatedReadOnlyBootstrapPrefersWorkspaceSymbolWhenAvailable(t *testing.T) {
	runner := &Runner{
		AgentName: "child:explorer",
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:   "delegated_read_only",
			AllowedTools: []string{"go_workspace_symbol", "search_content", "read_file"},
			Objective:    "Explain how `runChildTask` is used.\nNamed workspace scope: internal, cmd, prompts\nKey terms: runChildTask, child task",
		},
	}
	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Request: workspace.ToolRequest{Tool: "search_files", Pattern: "*.go"},
	})
	if decision.Request.Tool != "go_workspace_symbol" || decision.Request.Pattern != "runChildTask" {
		t.Fatalf("expected bootstrap redirect to go_workspace_symbol for exact symbol, got %#v", decision.Request)
	}
}

func TestDelegatedReadOnlyBootstrapPrefersWorkspaceSymbolOverDiscoveryForExactSymbolObjective(t *testing.T) {
	runner := &Runner{
		AgentName: "child:explorer",
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:   "delegated_read_only",
			AllowedTools: []string{"discovery_query", "go_workspace_symbol", "search_content", "read_file"},
			Objective:    "Explain how `runChildTask` is used.\nNamed workspace scope: internal, cmd, prompts\nKey terms: runChildTask, child task",
		},
	}
	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Request: workspace.ToolRequest{Tool: "search_files", Pattern: "*.go"},
	})
	if decision.Request.Tool != "go_workspace_symbol" || decision.Request.Pattern != "runChildTask" {
		t.Fatalf("expected bootstrap redirect to go_workspace_symbol for exact symbol even when discovery_query is available, got %#v", decision.Request)
	}
}

func TestDelegatedReadOnlyBootstrapPrefersDiscoveryQueryOverWorkspaceSymbolForBroadObjective(t *testing.T) {
	runner := &Runner{
		AgentName: "child:explorer",
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:   "delegated_read_only",
			AllowedTools: []string{"discovery_query", "go_workspace_symbol", "search_content", "read_file"},
			Objective:    "Explain how the harness uses sub-agents.\nNamed workspace scope: internal, cmd, prompts\nKey terms: harness, sub-agent, subagent",
		},
	}
	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Request: workspace.ToolRequest{Tool: "search_files", Pattern: "*.go"},
	})
	if decision.Request.Tool != "discovery_query" || decision.Request.Purpose != "architecture_boundary" {
		t.Fatalf("expected broad bootstrap redirect to discovery_query, got %#v", decision.Request)
	}
}

func TestDelegatedReadOnlyBootstrapDoesNotTreatPlainVerbAsExactSymbol(t *testing.T) {
	runner := &Runner{
		AgentName: "child:explorer",
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:   "delegated_read_only",
			AllowedTools: []string{"discovery_query", "go_workspace_symbol", "search_content", "read_file"},
			Objective:    "How does this harness utilize sub-agents?\nNamed workspace scope: internal/agent, internal/workspace, internal\nKey terms: utilize, sub-agent, task, delegate",
		},
	}
	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Request: workspace.ToolRequest{Tool: "search_files", Path: "internal/agent", Pattern: "*.go"},
	})
	if decision.Request.Tool != "discovery_query" {
		t.Fatalf("expected broad verb/key-term objective to use discovery_query, got %#v", decision.Request)
	}
	if decision.Request.Pattern == "utilize" {
		t.Fatalf("plain verb should not be treated as an exact symbol probe: %#v", decision.Request)
	}
}

func TestDelegatedReadOnlyBootstrapPatternIgnoresPromptGuidanceNoise(t *testing.T) {
	raw := "how does this harness utilize sub-agents\n" +
		"Named workspace scope: internal/agent, internal/workspace, internal\n" +
		"Start from source-of-truth implementation files before tests, README, or docs.\n" +
		"Return inspected paths, key findings, confidence, and named gaps."
	prompt := (&Runner{}).augmentReadOnlyChildPrompt(normalizeReadOnlyChildPrompt(raw))
	pattern := delegatedReadOnlyBootstrapPattern(prompt)
	if !strings.Contains(pattern, "sub[-_ ]?agents") {
		t.Fatalf("expected pattern to preserve the delegated question term, got %q", pattern)
	}
	for _, forbidden := range []string{"internal", "source", "truth", "inspected", "directly", "evidence"} {
		if strings.Contains(pattern, forbidden) {
			t.Fatalf("expected bootstrap pattern to ignore harness guidance term %q, got %q", forbidden, pattern)
		}
	}
}

func TestDelegatedReadOnlyTaskPromptUsesGenericImplementationScope(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"internal/transcript", "internal/app", "internal/agent"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	prompt := delegatedReadOnlyTaskPrompt("What does the debug log redacted mode do?", root)
	if strings.Contains(prompt, "internal/transcript, internal/app") {
		t.Fatalf("expected prompt scope not to special-case debug log features, got:\n%s", prompt)
	}
	if !strings.Contains(prompt, "Named workspace scope: internal/agent, internal") {
		t.Fatalf("expected generic implementation scope, got:\n%s", prompt)
	}
}

func TestDelegatedReadOnlyLikelySourceCandidatesSkipCmdWithoutEntrypointObjective(t *testing.T) {
	candidates := delegatedReadOnlyLikelySourceCandidates("Explain how this harness uses sub-agents.")
	for _, candidate := range candidates {
		if candidate == "cmd" {
			t.Fatalf("expected non-entrypoint architecture objective to skip cmd by default, got %#v", candidates)
		}
	}
}

func TestDelegatedReadOnlyLikelySourceCandidatesIncludeCmdForEntrypointObjective(t *testing.T) {
	candidates := delegatedReadOnlyLikelySourceCandidates("Explain which CLI entrypoints invoke delegated child tasks.")
	found := false
	for _, candidate := range candidates {
		if candidate == "cmd" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected entrypoint objective to include cmd scope, got %#v", candidates)
	}
}

func TestDelegatedReadOnlyBootstrapPatternDoesNotInjectDelegateForDebugFeature(t *testing.T) {
	objective := strings.Join([]string{
		"What does the debug log redacted mode do?",
		"Scope guidance:",
		"- Answer the delegated question directly once the evidence is sufficient.",
		"Key terms: debug, redacted",
	}, "\n")
	pattern := delegatedReadOnlyBootstrapPattern(objective)
	if !strings.Contains(pattern, "debug") || !strings.Contains(pattern, "redacted") {
		t.Fatalf("expected debug/redacted pattern, got %q", pattern)
	}
	if strings.Contains(pattern, "delegate") || strings.Contains(pattern, "task") {
		t.Fatalf("debug log feature pattern should not inherit generic delegated-scope terms, got %q", pattern)
	}
}

func TestMainArchitectureBootstrapDoesNotForceTaskDelegation(t *testing.T) {
	root := t.TempDir()
	runner := &Runner{
		AgentName: "main",
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "architecture",
			Contract:          "architecture_assessment",
			Objective:         "Explain how this harness uses sub-agents.",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"task", "list_files", "search_content", "read_file"},
			SearchPolicy:      []string{"prefer_discovery_query_for_architecture_boundary", "trace_boundaries_from_named_components"},
		},
	}
	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Request: workspace.ToolRequest{Tool: "list_files", Path: "."},
	})
	if decision.Request.Tool != "list_files" || decision.Request.Path != "." {
		t.Fatalf("expected architecture bootstrap to leave the first read decision local, got %#v", decision.Request)
	}
}

func TestMainArchitectureBootstrapDoesNotOverrideExactStructuredProbe(t *testing.T) {
	root := t.TempDir()
	runner := &Runner{
		AgentName: "main",
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "architecture",
			Contract:          "architecture_assessment",
			Objective:         "Explain how this harness uses sub-agents.",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"task", "go_read_symbol", "go_extract_body"},
			SearchPolicy:      []string{"prefer_discovery_query_for_architecture_boundary", "trace_boundaries_from_named_components"},
		},
	}
	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Request: workspace.ToolRequest{Tool: "go_read_symbol", Path: "internal/agent/runner_child_tasks.go", Pattern: "runChildTask"},
	})
	if decision.Request.Tool != "go_read_symbol" || decision.Request.Path != "internal/agent/runner_child_tasks.go" || decision.Request.Pattern != "runChildTask" {
		t.Fatalf("expected exact structured probe to stay local, got %#v", decision.Request)
	}
}

func TestDelegatedReadOnlyNamedGoReadRedirectsToScopedSearch(t *testing.T) {
	runner := &Runner{
		AgentName: "child:explorer",
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: "delegated_read_only",
			Objective:  "Explain how sub-agents are invoked.\nKey terms: task, sub-agent, subagent",
		},
	}
	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Request: workspace.ToolRequest{Tool: "read_file", Path: "internal/agent/runner.go"},
	})
	if decision.Request.Tool != "search_content" {
		t.Fatalf("expected named go-file read to redirect to search_content, got %#v", decision.Request)
	}
	if decision.Request.Path != "internal/agent/runner.go" {
		t.Fatalf("expected scoped search to stay on runner.go, got %#v", decision.Request)
	}
	for _, want := range []string{"task", "sub[-_ ]?agent", "subagent"} {
		if !strings.Contains(decision.Request.Pattern, want) {
			t.Fatalf("expected scoped search pattern to include %q, got %q", want, decision.Request.Pattern)
		}
	}
}

func TestDelegatedReadOnlyNamedGoReadPrefersOutlineWhenAvailable(t *testing.T) {
	runner := &Runner{
		AgentName: "child:explorer",
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:   "delegated_read_only",
			AllowedTools: []string{"go_file_outline", "search_content", "read_file"},
			Objective:    "Explain how sub-agents are invoked.\nKey terms: task, sub-agent, subagent",
		},
	}
	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Request: workspace.ToolRequest{Tool: "read_file", Path: "internal/agent/runner.go"},
	})
	if decision.Request.Tool != "go_file_outline" || decision.Request.Path != "internal/agent/runner.go" {
		t.Fatalf("expected named go-file read to redirect to go_file_outline, got %#v", decision.Request)
	}
}

func TestPostReadinessGoReadSymbolRedirectsToBodyAfterTruncatedRead(t *testing.T) {
	runner := &Runner{}
	results := []workspace.ToolResult{{
		Tool:    "go_read_symbol",
		Summary: "Read Runner.runChildTask in internal/agent/runner_child_tasks.go lines 16-135 (truncated from lines 16-516; full-body adapter: go_extract_body)",
		Output:  "func (r *Runner) runChildTask(...) {",
	}}
	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Request: workspace.ToolRequest{Tool: "go_read_symbol", Path: "internal/agent/runner_child_tasks.go", Pattern: "runChildTask"},
		Results: results,
	})
	if decision.Request.Tool != "go_extract_body" {
		t.Fatalf("expected truncated go_read_symbol follow-up to upgrade to go_extract_body, got %#v", decision.Request)
	}
}

func TestDelegatedReadOnlyIntegrationPivotRedirectsToSiblingImplementation(t *testing.T) {
	runner := &Runner{
		AgentName: "child:explorer",
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: "delegated_read_only",
			Objective:  "Explain how delegated child runners are created.\nKey terms: subagent, delegate, task",
		},
	}
	results := []workspace.ToolResult{
		{
			Tool:   "search_content",
			Output: "internal/agent/runner_child_tasks.go:16:func (r *Runner) runChildTask(...)\ninternal/agent/runner.go:574:result := r.runChildTask(ctx, req, opts)\n",
		},
		{
			Tool:    "read_file",
			Summary: "Read internal/agent/runner_child_tasks.go lines 16-130",
			Output:  "16:func (r *Runner) runChildTask(ctx context.Context, req workspace.ToolRequest, opts llama.ChatOptions) workspace.ToolResult {\n",
		},
	}
	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Request: workspace.ToolRequest{Tool: "read_file", Path: "internal/agent/runner_child_tasks.go", StartLine: 185, EndLine: 250},
		Results: results,
	})
	if decision.Request.Tool != "read_file" {
		t.Fatalf("expected pivot redirect to targeted read_file, got %#v", decision.Request)
	}
	if decision.Request.Path != "internal/agent/runner.go" {
		t.Fatalf("expected pivot redirect to sibling implementation file, got %#v", decision.Request)
	}
	if decision.Request.StartLine != 554 || decision.Request.EndLine != 654 {
		t.Fatalf("expected hit-window read around runner.go caller, got %#v", decision.Request)
	}
}

func TestDelegatedReadOnlyIntegrationPivotDoesNotOverrideStructuredProbe(t *testing.T) {
	runner := &Runner{
		AgentName: "child:explorer",
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: "delegated_read_only",
			Objective:  "Explain how delegated child runners are created.\nKey terms: subagent, delegate, task",
		},
	}
	results := []workspace.ToolResult{
		{
			Tool:   "search_content",
			Output: "internal/agent/runner_child_tasks.go:16:func (r *Runner) runChildTask(...)\ninternal/agent/runner.go:574:result := r.runChildTask(ctx, req, opts)\n",
		},
		{
			Tool:    "read_file",
			Summary: "Read internal/agent/runner_child_tasks.go lines 16-130",
			Output:  "16:func (r *Runner) runChildTask(ctx context.Context, req workspace.ToolRequest, opts llama.ChatOptions) workspace.ToolResult {\n",
		},
	}
	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Request: workspace.ToolRequest{Tool: "go_read_symbol", Path: "internal/agent/runner_child_tasks.go", Pattern: "runChildTask"},
		Results: results,
	})
	if decision.Request.Tool != "go_read_symbol" || decision.Request.Path != "internal/agent/runner_child_tasks.go" || decision.Request.Pattern != "runChildTask" {
		t.Fatalf("expected structured symbol probe to remain model-directed, got %#v", decision.Request)
	}
}

func TestDelegatedReadOnlyAnswerReadyDoesNotForceSynthesisAfterTwoImplementationReads(t *testing.T) {
	runner := &Runner{
		AgentName: "child:explorer",
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: "delegated_read_only",
			Objective:  "Explain how delegated child runners are created.\nKey terms: subagent, delegate, task",
		},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read internal/agent/runner_child_tasks.go lines 16-130",
			Output:  "16:func (r *Runner) runChildTask(ctx context.Context, req workspace.ToolRequest, opts llama.ChatOptions) workspace.ToolResult {\n",
		},
		{
			Tool:    "read_file",
			Summary: "Read internal/agent/runner.go lines 554-654",
			Output:  "574:\t\tresult := r.runChildTask(ctx, req, opts)\n",
		},
	}
	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Request: workspace.ToolRequest{Tool: "search_content", Path: "internal/agent", Pattern: "delegate|task"},
		Results: results,
	})
	if decision.RejectSummary != "" || decision.ForceAnswer != nil {
		t.Fatalf("expected delegated child evidence to remain model-directed, got %#v", decision)
	}
}

func TestSynthesizeDelegatedReadOnlyAnswerBuildsGroundedPacket(t *testing.T) {
	results := []workspace.ToolResult{
		{
			Tool:    "search_content",
			Output:  "internal/agent/runner.go:574:\t\tresult := r.runChildTask(ctx, req, opts)\ninternal/agent/runner_child_tasks.go:93:\t\t\t\"You are a read-only delegated child task.\"\n",
			Summary: "Found 2 matching line(s).",
		},
		{
			Tool:    "read_file",
			Summary: "Read internal/agent/runner_child_tasks.go lines 16-130",
			Output:  "16:func (r *Runner) runChildTask(ctx context.Context, req workspace.ToolRequest, opts llama.ChatOptions) workspace.ToolResult {\n93:\t\t\t\"You are a read-only delegated child task.\"\n",
		},
		{
			Tool:    "read_file",
			Summary: "Read internal/agent/runner.go lines 554-654",
			Output:  "574:\t\tresult := r.runChildTask(ctx, req, opts)\n",
		},
	}

	answer, ok := synthesizeDelegatedReadOnlyAnswer("Can you tell me how this harness uses subagents?", results)
	if !ok {
		t.Fatal("expected delegated read-only synthesis to succeed")
	}
	for _, want := range []string{
		"Source: delegated read-only evidence packet.",
		"Inspected paths:",
		"Key findings:",
		"Confidence:",
		"`internal/agent/runner.go`",
		"`internal/agent/runner_child_tasks.go`",
	} {
		if !strings.Contains(answer, want) {
			t.Fatalf("expected synthesized answer to contain %q, got %q", want, answer)
		}
	}
	for _, forbidden := range []string{
		"Partial findings from this turn",
		"Source: delegated subagent task packet.",
		"Discovery packet:",
		"Relevant source hits:",
	} {
		if strings.Contains(answer, forbidden) {
			t.Fatalf("expected composed user-facing synthesis without %q, got %q", forbidden, answer)
		}
	}
}

func TestDelegatedDiscoveryQueryEvidencePathsFeedSynthesizedAnswer(t *testing.T) {
	results := []workspace.ToolResult{
		{
			Tool:    "discovery_query",
			Summary: "Ran architecture_boundary discovery query with 10 layer(s); artifact: .idiom/discovery/queries/query.json",
			Output:  `{"purpose":"architecture_boundary","artifact_path":".idiom/discovery/queries/query.json","evidence":[{"name":"target_listing","summary":"Listed internal/agent.","output_excerpt":"internal/agent/final_answer_decorator.go\ninternal/agent/discovery_redirect.go\ninternal/agent/instructions.go"},{"name":"text_anchor_search","summary":"Found subagent handoff implementation paths.","output_excerpt":"internal/agent/runner_child_tasks.go:95: For broad architecture questions, prefer discovery_query first.\ninternal/agent/runner.go:574: result := r.runChildTask(ctx, req, opts)\n.idiom/discovery/queries/query.json"}]}`,
		},
	}

	paths := delegatedDiscoveryQueryEvidencePaths(results, 4)
	if len(paths) < 2 || paths[0] != "internal/agent/runner.go" || paths[1] != "internal/agent/runner_child_tasks.go" {
		t.Fatalf("expected text-anchor evidence paths to outrank target listing paths, got %#v", paths)
	}
	for _, want := range []string{"internal/agent/runner.go", "internal/agent/runner_child_tasks.go"} {
		if !containsRunnerString(paths, want) {
			t.Fatalf("expected discovery paths to contain %q, got %#v", want, paths)
		}
	}
	if containsRunnerString(paths, ".idiom/discovery/queries/query.json") {
		t.Fatalf("discovery artifact path should not displace implementation evidence, got %#v", paths)
	}

	answer, ok := synthesizeDelegatedReadOnlyAnswer("Explain how the harness uses subagents.", results)
	if !ok {
		t.Fatal("expected synthesized answer from discovery_query packet")
	}
	for _, want := range []string{"`internal/agent/runner.go`", "`internal/agent/runner_child_tasks.go`"} {
		if !strings.Contains(answer, want) {
			t.Fatalf("expected synthesized answer to cite %q, got:\n%s", want, answer)
		}
	}
	for _, want := range []string{
		"The strongest implementation evidence is in",
		"What this establishes:",
		"Text anchors:",
	} {
		if !strings.Contains(answer, want) {
			t.Fatalf("expected human-readable discovery synthesis to contain %q, got:\n%s", want, answer)
		}
	}
	for _, forbidden := range []string{`"layers_run"`, `"evidence"`, "{\n", "Discovery packet:", "Relevant source hits:", "Evidence used:", "Confidence:"} {
		if strings.Contains(answer, forbidden) {
			t.Fatalf("synthesized answer should not dump raw discovery JSON %q, got:\n%s", forbidden, answer)
		}
	}
}

func TestDelegatedDiscoveryQueryEvidencePathsPreferCoreRunnerFiles(t *testing.T) {
	results := []workspace.ToolResult{{
		Tool:    "discovery_query",
		Summary: "Ran architecture_boundary discovery query with 10 layer(s); artifact: .idiom/discovery/queries/query.json",
		Output:  `{"purpose":"architecture_boundary","artifact_path":".idiom/discovery/queries/query.json","evidence":[{"name":"text_anchor_search","summary":"Found discovery implementation paths.","output_excerpt":"internal/agent/modes.go:1: package agent\ninternal/agent/instructions.go:1: package agent\ninternal/agent/final_answer_decorator.go:1: package agent\ninternal/agent/answer_composer.go:18: func (AnswerComposer) Compose\ninternal/agent/runner.go:128: finish := func\ninternal/agent/runner_lane_policy.go:637: delegatedReadOnlyAnswerReadyAfterResult\ninternal/agent/discovery_redirect.go:1: package agent\ninternal/agent/runner_test.go:1: package agent"}]}`,
	}}

	paths := delegatedDiscoveryQueryEvidencePaths(results, 5)
	for _, want := range []string{"internal/agent/answer_composer.go", "internal/agent/runner.go", "internal/agent/runner_lane_policy.go"} {
		if !containsRunnerString(paths, want) {
			t.Fatalf("expected core path %q in top evidence paths, got %#v", want, paths)
		}
	}
	if containsRunnerString(paths, "internal/agent/modes.go") {
		t.Fatalf("incidental path displaced core runner evidence: %#v", paths)
	}
}

func TestDelegatedDiscoveryAnswerStaysGenericForDebugLogRedactedMode(t *testing.T) {
	results := []workspace.ToolResult{
		{
			Tool:    "discovery_query",
			Summary: "Ran architecture_boundary discovery query with 10 layer(s); artifact: .idiom/discovery/queries/debug.json",
			Output:  `{"purpose":"architecture_boundary","artifact_path":".idiom/discovery/queries/debug.json","evidence":[{"name":"text_anchor_search","summary":"Found debug redaction implementation paths.","output_excerpt":"internal/transcript/debuglog.go:18: DebugLogModeRedacted DebugLogMode = \"redacted\"\ninternal/transcript/debuglog.go:528: return redactedBlob(typed)\ninternal/app/model_state.go:469: case \"redacted\", \"full\":"}]}`,
		},
	}

	answer, ok := synthesizeDelegatedReadOnlyAnswer("What does the debug log redacted mode do?", results)
	if !ok {
		t.Fatal("expected synthesized answer from debug log evidence")
	}
	for _, want := range []string{
		"The strongest implementation evidence is in",
		"What this establishes:",
		"Text anchors:",
		"`internal/transcript/debuglog.go`",
		"`internal/app/model_state.go`",
	} {
		if !strings.Contains(answer, want) {
			t.Fatalf("expected generic synthesized answer to contain %q, got:\n%s", want, answer)
		}
	}
	for _, forbidden := range []string{"## What Stays Useful", "## What Gets Redacted", "Found 400 matching", `"evidence"`, "Discovery packet:", "Relevant source hits:", "Evidence used:", "Confidence:"} {
		if strings.Contains(answer, forbidden) {
			t.Fatalf("generic synthesis should not use a topic template or dump metadata %q:\n%s", forbidden, answer)
		}
	}
}

func TestAnswerComposerDoesNotLeakTelemetrySections(t *testing.T) {
	answer, ok := AnswerComposer{}.Compose(AnswerComposerInput{
		Question:      "Can you tell me how this harness uses subagents?",
		EvidencePaths: []string{"`internal/agent/runner.go`,", "internal/agent/runner_child_tasks.go"},
		Summaries: []delegatedDiscoverySummary{{
			Purpose:      "architecture_boundary",
			ArtifactPath: ".idiom/discovery/queries/query.json",
		}},
	})
	if !ok {
		t.Fatal("expected answer composer to produce generic evidence answer")
	}
	for _, forbidden := range []string{"Discovery packet:", "Relevant source hits:", "Evidence used:", "Confidence:", ".idiom/discovery"} {
		if strings.Contains(answer, forbidden) {
			t.Fatalf("answer composer leaked telemetry section %q:\n%s", forbidden, answer)
		}
	}
	for _, want := range []string{"The strongest implementation evidence is in", "Use those findings as the source of truth"} {
		if !strings.Contains(answer, want) {
			t.Fatalf("answer composer missing polished section %q:\n%s", want, answer)
		}
	}
	if !strings.Contains(answer, "`internal/agent/runner.go`, `internal/agent/runner_child_tasks.go`") {
		t.Fatalf("answer composer should format inline evidence paths cleanly:\n%s", answer)
	}
	for _, forbidden := range []string{"## Grounded Answer", "### Decision Guidance"} {
		if strings.Contains(answer, forbidden) {
			t.Fatalf("answer composer leaked internal heading %q:\n%s", forbidden, answer)
		}
	}
}

func TestAnswerComposerDoesNotTemplateQuestionClassesWithoutEvidenceSummary(t *testing.T) {
	answer, ok := AnswerComposer{}.Compose(AnswerComposerInput{
		Question: "How does this harness make use of sub-agents across read-only and workbranch modes?",
		EvidencePaths: []string{
			"internal/agent/runner.go",
			"internal/agent/runner_child_tasks.go",
			"internal/agent/answer_composer.go",
		},
	})
	if ok || strings.TrimSpace(answer) != "" {
		t.Fatalf("expected no topic-template answer without evidence summaries, ok=%v answer:\n%s", ok, answer)
	}
}

func TestAnswerComposerDoesNotSpecialCaseQuestionClassWithPathsOnly(t *testing.T) {
	answer, ok := AnswerComposer{}.Compose(AnswerComposerInput{
		Question: "How does this harness make use of sub-agents?",
		EvidencePaths: []string{
			"internal/agent/runner.go",
			"internal/agent/runner_child_tasks.go",
		},
	})
	if ok || strings.TrimSpace(answer) != "" {
		t.Fatalf("expected no question-class-specific answer with paths only, ok=%v answer:\n%s", ok, answer)
	}
}

func TestAnswerComposerDecoratesDiscoveryToolEvidenceGenerically(t *testing.T) {
	answer, ok := AnswerComposer{}.Compose(AnswerComposerInput{
		Question: "What can we add to our discovery tools in an effort at increasing their overall usefulness?",
		EvidencePaths: []string{
			"internal/agent/runner.go",
			"internal/agent/answer_composer.go",
			"internal/agent/runner_lane_policy.go",
			"internal/agent/discovery_redirect.go",
			"internal/agent/runner_test.go",
		},
		Summaries: []delegatedDiscoverySummary{{
			Purpose:      "architecture_boundary",
			ArtifactPath: ".idiom/discovery/queries/query.json",
			Evidence: []struct {
				Name          string `json:"name"`
				Tool          string `json:"tool"`
				Summary       string `json:"summary"`
				Error         string `json:"error,omitempty"`
				OutputExcerpt string `json:"output_excerpt,omitempty"`
			}{
				{Name: "text_anchor_search", Summary: "Found 178 matching line(s) (showing first 50). Bounded-read capability is available on returned handles, or narrow the pattern to see more."},
				{Name: "dependency_graph", Summary: "Collected Go dependency graph with go list -deps"},
			},
		}},
	})
	if !ok {
		t.Fatal("expected generic discovery-tool evidence answer")
	}
	for _, want := range []string{
		"The strongest implementation evidence is in",
		"What this establishes:",
		"Text anchors:",
		"Dependency graph:",
		"`internal/agent/answer_composer.go`",
		"`internal/agent/runner.go`",
		"`internal/agent/runner_lane_policy.go`",
	} {
		if !strings.Contains(answer, want) {
			t.Fatalf("expected generic discovery evidence answer to contain %q, got:\n%s", want, answer)
		}
	}
	for _, forbidden := range []string{"## Grounded Answer", "### Decision Guidance", "## Discovery Tool Improvements", "Purpose-specific answer blueprints", "What I found:", "text_anchor_search:", "Open a returned handle", "Discovery packet:", "discovery packet", "Evidence used:", "Confidence:"} {
		if strings.Contains(answer, forbidden) {
			t.Fatalf("generic discovery evidence answer leaked template/raw section %q:\n%s", forbidden, answer)
		}
	}
}

func TestAnswerComposerGenericFallbackDecoratesDiscoveryLayers(t *testing.T) {
	answer, ok := AnswerComposer{}.Compose(AnswerComposerInput{
		Question:      "How is routing decided?",
		EvidencePaths: []string{"internal/app/turn_planner.go", "internal/agent/modes.go"},
		Summaries: []delegatedDiscoverySummary{{
			Purpose:      "architecture_boundary",
			ArtifactPath: ".idiom/discovery/queries/query.json",
			Evidence: []struct {
				Name          string `json:"name"`
				Tool          string `json:"tool"`
				Summary       string `json:"summary"`
				Error         string `json:"error,omitempty"`
				OutputExcerpt string `json:"output_excerpt,omitempty"`
			}{
				{Name: "text_anchor_search", Summary: "Found routing anchors in internal/app/turn_planner.go. Bounded-read capability is available on returned handles, or narrow the pattern to see more."},
				{Name: "go_read_symbol", Summary: "Read classifyPromptRoute in internal/app/turn_planner.go lines 42-96"},
			},
		}},
	})
	if !ok {
		t.Fatal("expected generic composed answer")
	}
	for _, want := range []string{"The strongest implementation evidence is in", "What this establishes:", "Text anchors:", "Symbol read:"} {
		if !strings.Contains(answer, want) {
			t.Fatalf("expected decorated generic answer to contain %q, got:\n%s", want, answer)
		}
	}
	for _, forbidden := range []string{"## Grounded Answer", "### Decision Guidance", "What I found:", "text_anchor_search:", "go_read_symbol:", "Bounded-read capability is available", "Discovery packet:", "discovery packet", "Confidence:"} {
		if strings.Contains(answer, forbidden) {
			t.Fatalf("generic answer leaked raw discovery wording %q:\n%s", forbidden, answer)
		}
	}
}

func TestRunnerFinalBoundaryDecoratesModelFinalDiscoveryAnswer(t *testing.T) {
	root := t.TempDir()
	reply := "What I found:\n- text_anchor_search: Found 178 matching line(s) (showing first 50). Bounded-read capability is available on returned handles, or narrow the pattern to see more.\n\nDiscovery packet: `.idiom/discovery/queries/query.json`.\nConfidence: high"
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))
	defer server.Close()

	var roles []string
	runner := &Runner{
		Client: llama.NewClient(server.URL, 2*time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 2, MaxNoProgressTurns: 2},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "architecture",
			Contract:          "architecture_assessment",
			Objective:         "What can we add to our discovery tools in an effort at increasing their overall usefulness?",
			PermissionProfile: "read_only_workspace",
		},
		SeedResults: []workspace.ToolResult{{
			Tool:    "discovery_query",
			Summary: "Ran architecture_boundary discovery query with 10 layer(s); artifact: .idiom/discovery/queries/query.json",
			Output:  `{"purpose":"architecture_boundary","artifact_path":".idiom/discovery/queries/query.json","evidence":[{"name":"text_anchor_search","summary":"Found discovery tool implementation paths.","output_excerpt":"internal/agent/answer_composer.go:18: func (AnswerComposer) Compose\ninternal/agent/runner.go:128: finish := func\ninternal/agent/runner_lane_policy.go:637: delegatedReadOnlyAnswerReadyAfterResult"},{"name":"dependency_graph","summary":"Collected Go dependency graph with go list -deps"}]}`,
		}},
	}
	runner.OnEvent = func(event RunEvent) {
		roles = append(roles, event.Role)
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "What can we add to our discovery tools in an effort at increasing their overall usefulness?"}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if requests != 1 {
		t.Fatalf("expected one model request, got %d", requests)
	}
	for _, want := range []string{"retained source context", "`internal/agent/runner_lane_policy.go`", "`internal/agent/runner.go`"} {
		if !strings.Contains(answer, want) {
			t.Fatalf("expected decorated final answer to contain %q, got:\n%s", want, answer)
		}
	}
	for _, forbidden := range []string{"## Grounded Answer", "### Decision Guidance", "## Discovery Tool Improvements", "Purpose-specific answer blueprints", "What I found:", "text_anchor_search:", "Discovery packet", "Confidence:"} {
		if strings.Contains(answer, forbidden) {
			t.Fatalf("final boundary leaked raw answer section %q:\n%s", forbidden, answer)
		}
	}
	if !slices.Contains(roles, "answer_decorated") {
		t.Fatalf("expected answer_decorated event, got roles %v", roles)
	}
}

func TestFinalAnswerDecoratorSkipsChildInternalOutput(t *testing.T) {
	raw := "What I found:\n- text_anchor_search: Found paths.\n\nDiscovery packet: `.idiom/discovery/queries/query.json`."
	runner := &Runner{AgentName: "child:explorer"}
	answer := runner.decorateFinalAnswer(FinalAnswerDecorateInput{
		Answer:     raw,
		StopReason: "model_final",
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: "delegated_read_only",
			Objective:  "What can we add to discovery tools?",
		},
		Results: []workspace.ToolResult{{
			Tool:    "discovery_query",
			Summary: "Ran discovery query",
			Output:  `{"artifact_path":".idiom/discovery/queries/query.json","evidence":[{"name":"text_anchor_search","summary":"Found paths.","output_excerpt":"internal/agent/runner.go:128: finish"}]}`,
		}},
		UserFacing: runner.userFacingFinalAnswer(),
	})
	if answer != raw {
		t.Fatalf("expected child/internal answer to remain compact and undecorated, got:\n%s", answer)
	}
}

func TestFinalAnswerDecoratorPreservesBudgetExhaustionUncertainty(t *testing.T) {
	raw := "What I found:\n- text_anchor_search: Found paths.\n\nDiscovery packet: `internal/agent/runner.go`."
	runner := &Runner{}
	answer := runner.decorateFinalAnswer(FinalAnswerDecorateInput{
		Answer:     raw,
		StopReason: "tool_step_budget_exhausted",
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: "delegated_read_only",
			Objective:  "Explain how discovery tools are routed.",
		},
		Results: []workspace.ToolResult{{
			Tool:    "discovery_query",
			Summary: "Ran discovery query",
			Output:  `{"artifact_path":".idiom/discovery/queries/query.json","evidence":[{"name":"text_anchor_search","summary":"Found paths.","output_excerpt":"internal/agent/runner.go:128: finish"}]}`,
		}},
		UserFacing: true,
	})
	if answer != raw {
		t.Fatalf("expected budget-exhausted answer to preserve raw uncertainty, got:\n%s", answer)
	}
}

func TestFinalAnswerDecoratorDecoratesPostReadinessBudgetClosure(t *testing.T) {
	raw := "Partial findings from this turn (the budget was reached before a full synthesis):\n\nVerified context:\n- Ran discovery query\n"
	runner := &Runner{}
	answer := runner.decorateFinalAnswer(FinalAnswerDecorateInput{
		Answer:     raw,
		StopReason: "post_readiness_probe_budget_exhausted",
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: "delegated_read_only",
			Objective:  "Explain how discovery tools are routed.",
		},
		Results: []workspace.ToolResult{{
			Tool:    "discovery_query",
			Summary: "Ran discovery query",
			Output:  `{"artifact_path":".idiom/discovery/queries/query.json","evidence":[{"name":"text_anchor_search","summary":"Found routed implementation paths.","output_excerpt":"internal/agent/runner.go:128: finish"}]}`,
		}},
		UserFacing: true,
	})
	if strings.Contains(answer, "Partial findings from this turn") {
		t.Fatalf("expected post-readiness closure to use composed answer, got:\n%s", answer)
	}
	if strings.Contains(answer, "## Grounded Answer") || strings.Contains(answer, "### Decision Guidance") {
		t.Fatalf("expected composed answer without internal composer headings, got:\n%s", answer)
	}
	for _, want := range []string{"retained source context", "internal/agent/runner.go"} {
		if !strings.Contains(answer, want) {
			t.Fatalf("expected presentable post-readiness answer to contain %q, got:\n%s", want, answer)
		}
	}
}

func TestFinalAnswerDecoratorDoesNotLeakDelegatedPacketWhenComposerLacksDiscoverySummary(t *testing.T) {
	raw := "Question: how does this harness utilize sub-agents?\n" +
		"Source: delegated read-only evidence packet.\n\n" +
		"Inspected paths:\n" +
		"- `internal/agent/runner.go`\n" +
		"- `internal/agent/runner_child_tasks.go`\n" +
		"- `internal/agent/answer_composer.go`\n\n" +
		"Key findings:\n" +
		"- Child task completed\n" +
		"  The strongest implementation evidence is in `internal/agent/runner.go`, `internal/agent/runner_child_tasks.go`.\n" +
		"- Outlined 273 top-level declaration(s) in internal/agent/runner.go.\n\n" +
		"Confidence: high"
	runner := &Runner{}
	answer := runner.decorateFinalAnswer(FinalAnswerDecorateInput{
		Answer:     raw,
		StopReason: "post_readiness_probe_budget_exhausted",
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: "architecture",
			Objective:  "how does this harness utilize sub-agents?",
		},
		Results: []workspace.ToolResult{{
			Tool:    "task",
			Summary: "Child task completed",
			Output:  raw,
		}},
		UserFacing: true,
	})
	for _, forbidden := range []string{"Question:", "Source: delegated read-only evidence packet", "Inspected paths:", "Key findings:", "Confidence:", "Outlined 273"} {
		if strings.Contains(answer, forbidden) {
			t.Fatalf("final boundary leaked internal packet section %q:\n%s", forbidden, answer)
		}
	}
	for _, want := range []string{"retained source context", "`internal/agent/runner.go`", "`internal/agent/runner_child_tasks.go`"} {
		if !strings.Contains(answer, want) {
			t.Fatalf("expected presentable fallback to contain %q, got:\n%s", want, answer)
		}
	}
}

func TestFinalAnswerDecoratorUsesExactProbeEvidenceOverRetainedScopeSummary(t *testing.T) {
	raw := "The strongest implementation evidence is in `internal/agent/runner_child_tasks.go`. Use those findings as the source of truth for `how does this harness utilize sub-agents?`.\n\nWhat this establishes:\n- Text anchors: Found child-runner implementation paths."
	runner := &Runner{}
	answer := runner.decorateFinalAnswer(FinalAnswerDecorateInput{
		Answer:     raw,
		StopReason: "post_readiness_probe_budget_exhausted",
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: "architecture",
			Contract:   "architecture_assessment",
			Objective:  "how does this harness utilize sub-agents?",
		},
		Results: []workspace.ToolResult{
			{
				Tool:    "discovery_query",
				Summary: "Ran architecture_boundary discovery query",
				Output:  `{"artifact_path":".idiom/discovery/queries/query.json","evidence":[{"name":"text_anchor_search","summary":"Found child-runner implementation paths.","output_excerpt":"internal/agent/runner_child_tasks.go:16: func (r *Runner) runChildTask(ctx context.Context, req workspace.ToolRequest, opts llama.ChatOptions) workspace.ToolResult"}]}`,
			},
			{
				Tool:    "go_read_symbol",
				Summary: "Read Runner.runChildTask in internal/agent/runner_child_tasks.go lines 16-132",
				Output:  "func (r *Runner) runChildTask(ctx context.Context, req workspace.ToolRequest, opts llama.ChatOptions) workspace.ToolResult {\n\tprompt = normalizeReadOnlyChildPrompt(prompt)\n\tprompt = r.augmentReadOnlyChildPrompt(prompt)\n\tchild := &Runner{\n\t\tClient: r.Client,\n\t\tWorkspace: r.Workspace,\n\t\tPolicy: childPolicy,\n\t\tPermissionPolicy: readOnlyChildTaskPermissionPolicy(),\n\t\tDisableTaskTool: true,\n\t}\n\tanswer, err := child.Run(ctx, []llama.ChatMessage{{Role: \"user\", Content: prompt}}, opts, nil, nil, nil)\n\tanswer = augmentDelegatedReadOnlyPacket(answer, prompt, childResults)\n\treturn workspace.ToolResult{Tool: \"task\", Summary: \"Child task completed\", Output: strings.TrimSpace(answer)}\n}\n",
			},
		},
		UserFacing: true,
	})
	for _, want := range []string{
		"`Runner.runChildTask` in `internal/agent/runner_child_tasks.go`",
		"rewrites the delegated prompt",
		"executes the delegated question through a nested child runner",
		"folds the child answer and tool results back into the parent task-result/evidence flow",
	} {
		if !strings.Contains(answer, want) {
			t.Fatalf("expected exact-probe fallback answer to contain %q, got:\n%s", want, answer)
		}
	}
	for _, forbidden := range []string{
		"The strongest implementation evidence is in",
		"What this establishes:",
		"retained source context",
		"bounded answer",
	} {
		if strings.Contains(answer, forbidden) {
			t.Fatalf("expected exact-probe fallback answer to avoid %q, got:\n%s", forbidden, answer)
		}
	}
}

func TestFinalAnswerDecoratorPreservesSubstantiveModelAnswerOverEvidencePacket(t *testing.T) {
	raw := "The harness uses sub-agents through the `task` tool. The parent runner delegates broad read-only questions to a bounded child runner, and `Runner.runChildTask` creates that child with its own prompt, policy, tool budget, and read-only permissions before returning the child result as the parent's evidence packet.\n\nAdditional inspected paths:\n- `internal/agent/answer_composer.go`\n\nExact probes already inspected:\n- `runChildTask`"
	runner := &Runner{}
	answer := runner.decorateFinalAnswer(FinalAnswerDecorateInput{
		Answer:     raw,
		StopReason: "post_readiness_probe_budget_exhausted",
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: "architecture",
			Objective:  "how does this harness utilize sub-agents?",
		},
		Results: []workspace.ToolResult{{
			Tool:   "task",
			Output: "For `how does this harness utilize sub-agents?`, the gathered evidence points to these implementation surfaces and findings.\n\nEvidence:\n- Text anchors: Found 25 matching line(s).\n\nPrimary files: `internal/agent/runner.go`, `internal/agent/runner_child_tasks.go`.",
		}},
		UserFacing: true,
	})
	if !strings.Contains(answer, "The harness uses sub-agents through the `task` tool") {
		t.Fatalf("expected substantive model answer to be preserved, got:\n%s", answer)
	}
	for _, forbidden := range []string{"Additional inspected paths:", "Exact probes already inspected:", "the gathered evidence points to these implementation surfaces", "Evidence:\n- Text anchors:"} {
		if strings.Contains(answer, forbidden) {
			t.Fatalf("expected internal evidence packet/appendix %q to be removed, got:\n%s", forbidden, answer)
		}
	}
}

func TestChildPacketBoundaryNudgeNamesEvidencePaths(t *testing.T) {
	nudge := childPacketBoundaryNudge("Explain how this harness uses subagents.", []string{
		"internal/agent/discovery_redirect.go",
		"internal/workspace/discovery_query.go",
	}, 2)
	for _, want := range []string{
		"Why answer now is valid:",
		"Packet-named paths:",
		"`internal/agent/discovery_redirect.go`",
		"To continue, include `named_gap`",
		"`named_gap`",
		"repo-wide search will be rejected",
		"at most 2 targeted probes",
		"Answer now from the packet",
	} {
		if !strings.Contains(nudge, want) {
			t.Fatalf("expected child packet nudge to contain %q, got:\n%s", want, nudge)
		}
	}
}

func TestChildPacketBoundaryNudgeDisclosesActualProbeBudget(t *testing.T) {
	for _, tc := range []struct {
		budget int
		want   string
	}{
		{budget: 2, want: "at most 2 targeted probes"},
		{budget: 1, want: "one targeted probe"},
		{budget: 0, want: "no further reads"},
	} {
		nudge := childPacketBoundaryNudge("q", []string{"internal/agent/runner.go"}, tc.budget)
		if !strings.Contains(nudge, tc.want) {
			t.Fatalf("budget=%d: expected nudge to contain %q, got:\n%s", tc.budget, tc.want, nudge)
		}
	}
}

func TestChildPacketBoundaryNudgeFiltersNonFileArtifacts(t *testing.T) {
	nudge := childPacketBoundaryNudge("Explain how this harness uses subagents.", []string{
		".idiom/discovery/queries/query.json",
		"start_line/end_line",
		"internal/agent/runner_child_tasks.go",
		"idiom/internal/config",
	}, 2)
	// The static `.idiom/discovery/queries/` string in the contract is a
	// reference to where artifacts live, not a path emitted from the
	// evidence list. The test should fail only if the *evidence* list
	// leaks artifacts — check the "Packet-named paths:" line directly.
	for _, line := range strings.Split(nudge, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "Packet-named paths:") {
			continue
		}
		if strings.Contains(line, "start_line/end_line") || strings.Contains(line, "idiom/internal/config") || strings.Contains(line, ".idiom/discovery/queries/query.json") {
			t.Fatalf("expected packet-named paths line to drop non-file artifacts, got:\n%s", line)
		}
	}
	if !strings.Contains(nudge, "`internal/agent/runner_child_tasks.go`") {
		t.Fatalf("expected child packet nudge to keep real repository paths, got:\n%s", nudge)
	}
}

func TestDelegatedReadOnlyAnswerReadyNudgeIncludesClosureJustification(t *testing.T) {
	results := []workspace.ToolResult{{
		Tool:    "discovery_query",
		Summary: "Ran architecture_boundary discovery query",
		Output:  `{"artifact_path":".idiom/discovery/queries/query.json","evidence":[{"name":"go_read_symbol","summary":"Read subagent flow","output_excerpt":"internal/agent/runner.go:648 runChildTask\ninternal/agent/runner_child_tasks.go:16 runChildTask\ninternal/agent/answer_composer.go:18 Compose"}]}`,
	}}
	nudge := delegatedReadOnlyAnswerReadyNudge("Explain how this harness uses subagents.", results, 2)
	for _, want := range []string{
		"Answer now from the evidence above. The delegated read-only evidence is sufficient for this question.",
		"Why answer now is valid:",
		"Core evidence paths:",
		"Carry forward the inspected paths above",
		"To continue, include `named_gap`",
		"`named_gap`",
		"file-level raw reads",
		"`internal/agent/runner.go`",
	} {
		if !strings.Contains(nudge, want) {
			t.Fatalf("expected delegated readiness nudge to contain %q, got:\n%s", want, nudge)
		}
	}
}

func TestClosureProbeCandidatesExtractCallLikeSignals(t *testing.T) {
	results := []workspace.ToolResult{{
		Tool:    "discovery_query",
		Summary: "Ran architecture_boundary discovery query",
		Output:  `{"artifact_path":".idiom/discovery/queries/query.json","evidence":[{"name":"text_anchor_search","summary":"Found call sites","output_excerpt":"internal/agent/runner.go:574: result := r.runChildTask(ctx, req, opts)\ninternal/agent/runner_child_tasks.go:16: func (r *Runner) runChildTask(ctx context.Context, req workspace.ToolRequest, opts llama.ChatOptions) string"}]}`,
	}}
	candidates := closureProbeCandidates(results, 3)
	if !containsExactProbeCandidate(candidates, "runChildTask") {
		t.Fatalf("expected call-like discovery line to surface runChildTask, got %#v", candidates)
	}
}

func TestDelegatedClosureEvidencePathsPreserveDiscoveryPacketOrderAmongEqualPriorityFiles(t *testing.T) {
	results := []workspace.ToolResult{{
		Tool:    "discovery_query",
		Summary: "Ran architecture_boundary discovery query",
		Output:  `{"artifact_path":".idiom/discovery/queries/query.json","evidence":[{"name":"text_anchor_search","summary":"Found handoff files","output_excerpt":"internal/agent/runner.go:574: result := r.runChildTask(ctx, req, opts)\ninternal/agent/runner_child_tasks.go:16: func (r *Runner) runChildTask(ctx context.Context, req workspace.ToolRequest, opts llama.ChatOptions) string\ninternal/agent/answer_composer.go:18: func (AnswerComposer) Compose(input AnswerComposerInput) (string, bool)"}]}`,
	}}
	paths := delegatedClosureEvidencePaths("Explain how this harness uses subagents.", results, 4)
	if len(paths) < 3 {
		t.Fatalf("expected multiple closure evidence paths, got %#v", paths)
	}
	if paths[0] != "internal/agent/runner.go" || paths[1] != "internal/agent/runner_child_tasks.go" {
		t.Fatalf("expected discovery-ranked runner paths to stay ahead of ancillary files, got %#v", paths)
	}
}

func TestDelegatedReadOnlyDiminishingReturnsNudgeEscalates(t *testing.T) {
	results := []workspace.ToolResult{{
		Tool:    "discovery_query",
		Summary: "Ran architecture_boundary discovery query",
		Output:  `{"artifact_path":".idiom/discovery/queries/query.json","evidence":[{"name":"go_read_symbol","summary":"Read subagent flow","output_excerpt":"internal/agent/runner.go:648 runChildTask\ninternal/agent/runner_child_tasks.go:16 runChildTask"}]}`,
	}}
	first := delegatedReadOnlyDiminishingReturnsNudge("Explain how this harness uses subagents.", results, 1)
	second := delegatedReadOnlyDiminishingReturnsNudge("Explain how this harness uses subagents.", results, 2)
	if !strings.Contains(first, "Post-readiness probes used: 1") {
		t.Fatalf("expected first diminishing-returns nudge to count one used probe, got:\n%s", first)
	}
	if !strings.Contains(first, "`internal/agent/runner.go`") {
		t.Fatalf("expected first diminishing-returns nudge to surface inspected paths, got:\n%s", first)
	}
	if !strings.Contains(second, "Post-readiness probes used: 2") {
		t.Fatalf("expected second diminishing-returns nudge to count two used probes, got:\n%s", second)
	}
}

func TestDelegatedReadOnlyReadRedirectUsesPriorSearchHitWindow(t *testing.T) {
	runner := &Runner{
		AgentName: "child:explorer",
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: "delegated_read_only",
			Objective:  "Explain how delegated child runners are created.\nKey terms: subagent, delegate, task",
		},
	}
	results := []workspace.ToolResult{
		{
			Tool:   "search_content",
			Output: "internal/agent/runner.go:574:\t\tresult := r.runChildTask(ctx, req, opts)\n",
		},
	}
	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Request: workspace.ToolRequest{Tool: "read_file", Path: "internal/agent/runner.go", StartLine: 9, EndLine: 109},
		Results: results,
	})
	if decision.Request.Tool != "read_file" || decision.Request.Path != "internal/agent/runner.go" {
		t.Fatalf("expected redirected hit-window read on runner.go, got %#v", decision.Request)
	}
	if decision.Request.StartLine != 554 || decision.Request.EndLine != 654 {
		t.Fatalf("expected redirected hit-window range, got %#v", decision.Request)
	}
}

func TestNormalizeReadOnlyChildPromptPreservesNamedScope(t *testing.T) {
	prompt := normalizeReadOnlyChildPrompt("How does this harness use sub-agents?\nNamed workspace scope: internal, cmd\nReturn inspected paths and key findings.")
	if !strings.Contains(prompt, "Named workspace scope: internal, cmd") {
		t.Fatalf("expected normalized child prompt to preserve named scope, got %q", prompt)
	}
}

func TestNormalizeReadOnlyChildPromptStripsBroadExplorationClauses(t *testing.T) {
	prompt := normalizeReadOnlyChildPrompt(strings.Join([]string{
		"How does this harness use sub-agents? Explore the cmd directory, main entry points, and look for patterns in startup flow.",
		"Focus on Go code in cmd/ and any agent-related packages.",
		"Named workspace scope: internal, cmd",
		"Return inspected paths and key findings.",
	}, "\n"))
	if !strings.HasPrefix(prompt, "How does this harness use sub-agents?") {
		t.Fatalf("expected normalized child prompt to keep the delegated question first, got %q", prompt)
	}
	for _, unwanted := range []string{"Explore the cmd directory", "main entry points", "Focus on Go code in cmd/"} {
		if strings.Contains(prompt, unwanted) {
			t.Fatalf("expected normalized child prompt to strip broad exploration clause %q, got %q", unwanted, prompt)
		}
	}
	if !strings.Contains(prompt, "Named workspace scope: internal, cmd") {
		t.Fatalf("expected normalized child prompt to preserve named scope, got %q", prompt)
	}
}

func TestReadOnlyChildKeyTermsIncludeLaterSymbolHints(t *testing.T) {
	terms := readOnlyChildKeyTerms(strings.Join([]string{
		"Explain how delegated child runners are created.",
		"Focus on runChildTask and runWorkbranchChildTask.",
	}, "\n"))
	for _, want := range []string{"runchildtask", "runworkbranchchildtask"} {
		found := false
		for _, term := range terms {
			if term == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected key terms to preserve later symbol hint %q, got %#v", want, terms)
		}
	}
}

func TestBuildRunnerNextActionStatePrefersClosureAfterDelegatedReadiness(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: "delegated_read_only",
		Objective:  "Explain how this harness uses subagents.",
	}
	results := []workspace.ToolResult{
		{
			Tool:    "discovery_query",
			Summary: "Ran architecture_boundary discovery query",
			Output:  `{"artifact_path":".idiom/discovery/queries/query.json","evidence":[{"path":"internal/agent/runner.go","excerpt":"completion gate"},{"path":"internal/agent/runner_child_tasks.go","excerpt":"task child delegated_read_only"}]}`,
		},
		{
			Tool:    "search_content",
			Summary: "Found 10 matching line(s)",
			Output:  "internal/agent/runner.go:2228: completion gate\ninternal/agent/runner.go:2247: final answer rejected\n",
		},
	}
	state := buildRunnerNextActionState(
		"child:explorer",
		3,
		"tool_result",
		workspace.ToolRequest{Tool: "search_content", Path: "internal/agent/runner.go"},
		results[1],
		results,
		false,
		brief,
	)
	if !containsRunnerString(state.Constraints, "answer_readiness_reached") {
		t.Fatalf("expected delegated readiness closure bias, got %#v", state)
	}
	if containsRunnerString(state.Constraints, "completion_gate_active") {
		t.Fatalf("search output should not fabricate completion gate state, got %#v", state)
	}
	if containsRunnerString(state.InvalidNextActions, "final_answer_without_satisfying_completion_gate") {
		t.Fatalf("closure-biased state should not block answering on a fabricated gate, got %#v", state)
	}
	if len(state.NextValidActions) < 1 || state.NextValidActions[0] != "answer directly from the evidence already gathered" {
		t.Fatalf("expected answer-first closure bias, got %#v", state.NextValidActions)
	}
	for _, want := range []string{
		"internal/agent/runner.go",
		"internal/agent/runner_child_tasks.go",
	} {
		if !containsRunnerString(state.EvidencePaths, want) {
			t.Fatalf("expected delegated closure state to preserve evidence path %q, got %#v", want, state.EvidencePaths)
		}
	}
}

func TestBuildRunnerNextActionStatePrefersClosureAfterChildPacket(t *testing.T) {
	state := buildRunnerNextActionState(
		"main",
		1,
		"child_task_result",
		workspace.ToolRequest{Tool: "task", Mode: "read_only", Agent: "explorer"},
		workspace.ToolResult{Tool: "task", Summary: "Child task completed", Output: "Inspected paths: internal/agent/runner.go"},
		[]workspace.ToolResult{{Tool: "task", Summary: "Child task completed", Output: "Inspected paths: internal/agent/runner.go"}},
		false,
		workerruntime.WorkerRuntimeBrief{TaskFamily: "architecture", Objective: "Explain how subagents work."},
	)
	if !containsRunnerString(state.Constraints, "child_packet_available") {
		t.Fatalf("expected child-packet closure bias, got %#v", state)
	}
	if len(state.NextValidActions) == 0 || state.NextValidActions[0] != "answer directly from the child packet if it already resolves the question" {
		t.Fatalf("expected child packet answer-first guidance, got %#v", state.NextValidActions)
	}
	if !containsRunnerString(state.InvalidNextActions, "open-ended discovery after a child packet without naming a concrete gap") {
		t.Fatalf("expected anti-drift invalid action, got %#v", state.InvalidNextActions)
	}
}

func TestBuildRunnerNextActionStateUsesClosureConstraintsForSuccessfulPostChildSearch(t *testing.T) {
	results := []workspace.ToolResult{
		{
			Tool:   "task",
			Output: "Question: explain subagents\nSource: delegated read-only evidence packet.\n\nInspected paths:\n- `internal/agent/runner.go`\n- `internal/agent/answer_composer.go`\n",
		},
		{
			Tool:    "search_content",
			Summary: "Found 12 matching line(s) (showing first 12).",
			Output:  "internal/agent/answer_composer.go:73:synthesizeDelegatedReadOnlyAnswer\ninternal/agent/runner.go:648:runChildTask\n",
		},
	}
	state := buildRunnerNextActionState(
		"main",
		5,
		"tool_result",
		workspace.ToolRequest{Tool: "search_content", Pattern: "harness|sub.?agent"},
		results[1],
		results,
		false,
		workerruntime.WorkerRuntimeBrief{TaskFamily: "architecture", Objective: "Explain how subagents work."},
	)
	if !containsRunnerString(state.Constraints, "child_packet_available") {
		t.Fatalf("expected child-packet closure constraint, got %#v", state)
	}
	for _, forbidden := range []string{
		"edit_file if the target is already clear",
		"targeted verification evidence",
		"update task_ledger or answer with the concrete blocker",
	} {
		if containsRunnerString(state.NextValidActions, forbidden) {
			t.Fatalf("expected closure-constrained next actions to omit %q, got %#v", forbidden, state.NextValidActions)
		}
	}
}

func TestBuildRunnerNextActionStatePreservesDurableChildPacketClosureAfterSuccessfulOutline(t *testing.T) {
	state := buildRunnerNextActionState(
		"main",
		6,
		"tool_result",
		workspace.ToolRequest{Tool: "go_file_outline", Path: "internal/agent/runner.go"},
		workspace.ToolResult{
			Tool:    "go_file_outline",
			Summary: "Outlined 12 top-level declaration(s) in internal/agent/runner.go.",
			Output:  "package agent\ndeclarations:\n- runChildTask lines 640-700",
		},
		[]workspace.ToolResult{{
			Tool:    "go_file_outline",
			Summary: "Outlined 12 top-level declaration(s) in internal/agent/runner.go.",
			Output:  "package agent\ndeclarations:\n- runChildTask lines 640-700",
		}},
		false,
		workerruntime.WorkerRuntimeBrief{
			TaskFamily:    "architecture",
			Objective:     "Explain how this harness uses subagents.",
			Constraints:   []string{"child_packet_available"},
			EvidencePaths: []string{"internal/agent/runner.go", "internal/agent/runner_child_tasks.go"},
		},
	)
	if !containsRunnerString(state.Constraints, "child_packet_available") {
		t.Fatalf("expected durable child-packet closure constraint, got %#v", state)
	}
	for _, forbidden := range []string{
		"read-symbol capability with one outlined declaration name",
		"references or calls-into capability on a named declaration",
		"go_move_declarations",
	} {
		if containsRunnerString(state.NextValidActions, forbidden) {
			t.Fatalf("expected durable closure state to omit %q, got %#v", forbidden, state.NextValidActions)
		}
	}
}

func TestDelegatedReadOnlySecondaryTestReadRedirectsToImplementation(t *testing.T) {
	runner := &Runner{
		AgentName: "child:explorer",
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: "delegated_read_only",
			Objective:  "Explain how sub-agents are invoked and executed in this harness.\nKey terms: harness, sub-agent, subagent",
		},
	}
	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Request: workspace.ToolRequest{Tool: "read_file", Path: "internal/agent/runner_test.go", StartLine: 3140, EndLine: 3220},
	})
	if decision.Request.Tool != "search_content" {
		t.Fatalf("expected secondary test evidence to redirect to implementation search, got %#v", decision.Request)
	}
	if decision.Request.Path != "internal/agent/runner.go" {
		t.Fatalf("expected redirected search to target sibling implementation file, got %#v", decision.Request)
	}
	for _, want := range []string{"sub[-_ ]?agent", "invoke", "execute"} {
		if !strings.Contains(decision.Request.Pattern, want) {
			t.Fatalf("expected redirected implementation search pattern to include %q, got %q", want, decision.Request.Pattern)
		}
	}
	for _, forbidden := range []string{"delegate", "task"} {
		if strings.Contains(decision.Request.Pattern, forbidden) {
			t.Fatalf("expected redirected implementation search pattern not to inject scenario terms %q, got %q", forbidden, decision.Request.Pattern)
		}
	}
}

func TestCompactOutputRespectsLineBudget(t *testing.T) {
	got := compactOutput("a\nb\nc", 2)
	if got != "a\nb\n..." {
		t.Fatalf("unexpected compact output %q", got)
	}
	if got := compactOutput("a\nb", 0); got != "a\nb" {
		t.Fatalf("expected zero budget to preserve output, got %q", got)
	}
}

// Fix 7 (real subagent spawning via the task tool) — exercises
// docs/opencode-fixes-design.md §7. Verifies that the parent's `task` call
// launches an isolated child Runner, that the child actually drives the
// model loop (tool call plus answer), and that the child's intermediate
// messages do not pollute the parent's conversation.
func TestRunnerTaskToolSpawnsIsolatedReadOnlyChild(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "src/widget.go", "package widget\n"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	replies := []string{
		`{"tool":"task","agent":"scout","mode":"read_only","prompt":"list .go files under src/"}`,
		`{"tool":"list_files","path":"src"}`,
		"Found src/widget.go.",
		"Parent inspected scout's report and concluded.",
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request #%d", requests)
		}
		reply := replies[requests]
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))
	defer server.Close()

	mgr := workspace.NewManager(config.Workspace{
		Root:                root,
		CWD:                 root,
		MaxDirectoryEntries: 64,
	})
	var childRequests, childResults int
	runner := &Runner{
		Client:    llama.NewClient(server.URL, 2*time.Second),
		Workspace: mgr,
		Policy:    RunPolicy{MaxToolSteps: 4, MaxNoProgressTurns: 3},
		OnEvent: func(event RunEvent) {
			switch event.Role {
			case "child_tool_request":
				childRequests++
			case "child_tool_result":
				childResults++
			}
		},
	}
	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "delegate a read-only probe then report"}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if answer != "Parent inspected scout's report and concluded." {
		t.Fatalf("unexpected parent answer: %q", answer)
	}
	if childRequests == 0 || childResults == 0 {
		t.Fatalf("expected isolated child tool events to fire (requests=%d results=%d)", childRequests, childResults)
	}
	if requests != 4 {
		t.Fatalf("expected 4 chat requests (1 parent spawn + 2 child steps + 1 parent final), got %d", requests)
	}
}

func TestRunnerRequiresChildTaskForBroadEvidenceReads(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "agent"), 0o755); err != nil {
		t.Fatalf("mkdir internal/agent: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "internal", "agent", "runner.go"), []byte("package agent\n\nfunc RunnerHotPath() {}\n"), 0o644); err != nil {
		t.Fatalf("write runner.go: %v", err)
	}

	replies := []string{
		`{"tool":"read_file","path":"internal/agent/runner.go"}`,
		`{"tool":"search_files","pattern":"runner","path":"."}`,
		`{"tool":"read_file","path":"internal/agent/runner.go","start_line":1,"end_line":3}`,
		`{"tool":"list_files","path":"."}`,
		`{"tool":"read_file","path":"internal/agent/runner.go"}`,
		`{"tool":"list_files","path":"."}`,
		`{"tool":"task","agent":"explorer","mode":"read_only","prompt":"Inspect internal/agent/runner.go for performance evidence and report paths."}`,
		"Inspected paths: internal/agent/runner.go. No performance issue found.",
		"No performance risk stands out in internal/agent/runner.go from the delegated evidence. Confidence: medium.",
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request #%d", requests)
		}
		reply := replies[requests]
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))
	defer server.Close()

	var rejected bool
	runner := &Runner{
		Client: llama.NewClient(server.URL, 2*time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:                root,
			CWD:                 root,
			MaxDirectoryEntries: 64,
		}),
		Policy: RunPolicy{MaxToolSteps: 8, MaxNoProgressTurns: 6},
	}
	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Review performance risks in this workspace."}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			if strings.Contains(event.Result.Summary, "Broad exploration after delegation nudge blocked") {
				rejected = true
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if answer != "No performance risk stands out in internal/agent/runner.go from the delegated evidence. Confidence: medium." {
		t.Fatalf("unexpected answer: %q", answer)
	}
	if !rejected {
		t.Fatal("expected broad exploration to be rejected after the delegation nudge")
	}
	if requests != len(replies) {
		t.Fatalf("expected parent to take one post-child decision turn, used %d/%d replies", requests, len(replies))
	}
}

func TestRunnerReturnsChildPacketBeforeBroadRediscovery(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "agent"), 0o755); err != nil {
		t.Fatalf("mkdir internal/agent: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "internal", "agent", "runner.go"), []byte("package agent\n\nfunc RunnerHotPath() {}\n"), 0o644); err != nil {
		t.Fatalf("write runner.go: %v", err)
	}

	replies := []string{
		`{"tool":"read_file","path":"internal/agent/runner.go"}`,
		`{"tool":"search_files","pattern":"runner","path":"."}`,
		`{"tool":"read_file","path":"internal/agent/runner.go","start_line":1,"end_line":3}`,
		`{"tool":"list_files","path":"."}`,
		`{"tool":"task","agent":"explorer","mode":"read_only","prompt":"Inspect performance evidence in internal/agent/runner.go and report paths."}`,
		"Inspected paths: internal/agent/runner.go. No performance issue found.",
		"No performance risk stands out in internal/agent/runner.go from the delegated evidence. Confidence: medium.",
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request #%d", requests)
		}
		reply := replies[requests]
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))
	defer server.Close()

	runner := &Runner{
		Client: llama.NewClient(server.URL, 2*time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:                root,
			CWD:                 root,
			MaxDirectoryEntries: 64,
		}),
		Policy: RunPolicy{MaxToolSteps: 8, MaxNoProgressTurns: 6},
	}
	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Review performance risks in this workspace."}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if answer != "No performance risk stands out in internal/agent/runner.go from the delegated evidence. Confidence: medium." {
		t.Fatalf("unexpected answer: %q", answer)
	}
	if requests != len(replies) {
		t.Fatalf("expected parent to take one post-child decision turn, used %d/%d replies", requests, len(replies))
	}
}

func TestRunnerReturnsFirstReadOnlyChildPacketWithoutSecondDelegation(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "agent"), 0o755); err != nil {
		t.Fatalf("mkdir internal/agent: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "internal", "agent", "runner.go"), []byte("package agent\n\nfunc RunnerHotPath() {}\n"), 0o644); err != nil {
		t.Fatalf("write runner.go: %v", err)
	}

	replies := []string{
		`{"tool":"task","agent":"explorer","mode":"read_only","prompt":"Inspect internal/agent/runner.go and summarize."}`,
		"Inspected paths: internal/agent/runner.go. Key findings: child packet ready.",
		"internal/agent/runner.go is the inspected path and the delegated child packet did not surface a deeper issue. Confidence: medium.",
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request #%d", requests)
		}
		reply := replies[requests]
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))
	defer server.Close()

	runner := &Runner{
		Client: llama.NewClient(server.URL, 2*time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 6, MaxNoProgressTurns: 4},
	}
	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Explain how this harness delegates work."}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if answer != "internal/agent/runner.go is the inspected path and the delegated child packet did not surface a deeper issue. Confidence: medium." {
		t.Fatalf("unexpected answer: %q", answer)
	}
	if requests != len(replies) {
		t.Fatalf("expected parent to decide after the child packet, used %d/%d replies", requests, len(replies))
	}
}

func TestRunnerDirectAnswerReturnsChildPacketWithoutParentFollowups(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "workspace"), 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "internal", "workspace", "tool_manifest.go"), []byte("package workspace\n"), 0o644); err != nil {
		t.Fatalf("write tool manifest: %v", err)
	}

	replies := []string{
		`{"tool":"task","agent":"explorer","mode":"read_only","prompt":"Inspect internal/workspace/tool_manifest.go and explain how subagents are wired."}`,
		`{"tool":"search_files","pattern":"*tool_manifest*"}`,
		`{"tool":"read_file","path":"internal/workspace/tool_manifest.go","start_line":1,"end_line":20}`,
		"Partial findings from this turn (the budget was reached before a full synthesis):\n\nVerified context:\n- Read internal/workspace/tool_manifest.go lines 1-20\n- Found task tool manifest entries for delegated child work\n",
		"Subagents are wired through the task tool; the delegated child inspects internal/workspace/tool_manifest.go for the task manifest entries. Confidence: medium.",
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request #%d", requests)
		}
		reply := replies[requests]
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))
	defer server.Close()

	runner := &Runner{
		Client: llama.NewClient(server.URL, 2*time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 6, MaxNoProgressTurns: 4},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "direct_answer",
			Contract:          "direct_answer",
			Objective:         "Explain how subagents work in this harness.",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"task", "search_files", "read_file"},
		},
	}
	var roles []string
	runner.OnEvent = func(event RunEvent) {
		roles = append(roles, event.Role)
	}
	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Explain how subagents work in this harness."}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, want := range []string{
		"Subagents are wired through the task tool",
		"internal/workspace/tool_manifest.go",
	} {
		if !strings.Contains(answer, want) {
			t.Fatalf("expected direct answer to include %q, got %q", want, answer)
		}
	}
	if strings.Contains(strings.ToLower(answer), "partial findings from this turn") {
		t.Fatalf("expected parent synthesis instead of raw child packet, got %q", answer)
	}
	if strings.Contains(answer, "f23 ") {
		t.Fatalf("unexpected answer: %q", answer)
	}
	for _, want := range []string{"readiness_check", "final_answer_critic"} {
		if !slices.Contains(roles, want) {
			t.Fatalf("expected final acceptance signal %q, got roles %v", want, roles)
		}
	}
	if requests != len(replies) {
		t.Fatalf("expected parent to synthesize once after the child packet, used %d/%d replies", requests, len(replies))
	}
}

func TestRunnerArchitectureReturnsChildPacketWithoutParentFollowups(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "agent"), 0o755); err != nil {
		t.Fatalf("mkdir agent dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "internal", "agent", "runner_child_tasks.go"), []byte("package agent\n"), 0o644); err != nil {
		t.Fatalf("write runner child tasks: %v", err)
	}

	replies := []string{
		`{"tool":"task","agent":"explorer","mode":"read_only","prompt":"Inspect internal/agent/runner_child_tasks.go and explain how delegated child runners are created."}`,
		`{"tool":"search_files","pattern":"*runner_child_tasks*","path":"internal/agent"}`,
		`{"tool":"read_file","path":"internal/agent/runner_child_tasks.go","start_line":1,"end_line":20}`,
		"Partial findings from this turn (the budget was reached before a full synthesis):\n\nVerified context:\n- Read f23 internal/agent/runner_child_tasks.go lines 1-20\n- Found task child runner creation in internal/agent/runner_child_tasks.go\n",
		"Delegated child runners are created in internal/agent/runner_child_tasks.go and invoked from the parent runner loop. Confidence: medium.",
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request #%d", requests)
		}
		reply := replies[requests]
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))
	defer server.Close()

	runner := &Runner{
		Client: llama.NewClient(server.URL, 2*time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 6, MaxNoProgressTurns: 4},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "architecture",
			Contract:          "architecture_assessment",
			Objective:         "Explain how delegated child runners are created in this harness.",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"task", "search_files", "read_file"},
		},
	}
	var roles []string
	runner.OnEvent = func(event RunEvent) {
		roles = append(roles, event.Role)
	}
	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Explain how delegated child runners are created in this harness."}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Contains(answer, "f23 ") {
		t.Fatalf("expected normalized packet in final answer, got %q", answer)
	}
	if strings.Contains(strings.ToLower(answer), "partial findings from this turn") {
		t.Fatalf("expected parent synthesis instead of raw child packet, got %q", answer)
	}
	if !strings.Contains(answer, "runner_child_tasks.go") {
		t.Fatalf("unexpected answer: %q", answer)
	}
	for _, want := range []string{"readiness_check", "final_answer_critic"} {
		if !slices.Contains(roles, want) {
			t.Fatalf("expected final acceptance signal %q, got roles %v", want, roles)
		}
	}
	if requests != len(replies) {
		t.Fatalf("expected parent to synthesize once after the child packet, used %d/%d replies", requests, len(replies))
	}
}

func TestRunnerChildDiscoveryPacketLetsChildDecideFollowupAfterDiscovery(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "agent"), 0o755); err != nil {
		t.Fatalf("mkdir agent dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "internal", "agent", "runner.go"), []byte("package agent\n\nfunc runChildTask() { _ = \"subagents task delegate\" }\n"), 0o644); err != nil {
		t.Fatalf("write runner.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "internal", "agent", "runner_child_tasks.go"), []byte("package agent\n\nfunc childInstructions() { _ = \"subagents task delegate\" }\n"), 0o644); err != nil {
		t.Fatalf("write runner_child_tasks.go: %v", err)
	}

	replies := []string{
		`{"tool":"task","agent":"explorer","mode":"read_only","prompt":"Explain how this harness uses subagents."}`,
		`{"tool":"search_content","path":"internal/agent","pattern":"subagents|task|delegate","glob":"*.go"}`,
		"## Grounded Answer\n\n### Evidence Highlights\n- Text anchors: Found implementation paths.\n\nPrimary files: `internal/agent/runner.go`, `internal/agent/runner_child_tasks.go`.",
		"The harness uses subagents through the task tool, primarily in internal/agent/runner.go and internal/agent/runner_child_tasks.go. Confidence: high.",
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request #%d", requests)
		}
		reply := replies[requests]
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))
	defer server.Close()

	var roles []string
	runner := &Runner{
		Client: llama.NewClient(server.URL, 2*time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 6, MaxNoProgressTurns: 4},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Lane:              "architecture",
			TaskFamily:        "architecture",
			Contract:          "architecture_assessment",
			Objective:         "Explain how this harness uses subagents.",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"task", "search_content", "read_file", "go_file_outline"},
			SearchPolicy:      []string{"delegate_broad_discovery_when_needed"},
		},
	}
	runner.OnEvent = func(event RunEvent) {
		roles = append(roles, event.Role)
	}
	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Explain how this harness uses subagents."}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(answer, "task tool") {
		t.Fatalf("unexpected answer: %q", answer)
	}
	for _, forbidden := range []string{`"layers_run"`, "Partial findings from this turn", "Discovery packet:", "Relevant source hits:", "Evidence used:"} {
		if strings.Contains(answer, forbidden) {
			t.Fatalf("unexpected answer with %q: %q", forbidden, answer)
		}
	}
	if !slices.Contains(roles, "child_delegated_read_only_answer_ready") {
		t.Fatalf("expected child answer-ready event, got roles %v", roles)
	}
	if !slices.Contains(roles, "child_delegated_read_only_answer_ready_nudge") {
		t.Fatalf("expected delegated read-only answer-ready nudge, got roles %v", roles)
	}
	if requests != len(replies) {
		t.Fatalf("expected child follow-up plus parent decision turn, used %d/%d replies", requests, len(replies))
	}
}

func TestDelegatedReadOnlyAnswerReadyAfterDiscoveryQuery(t *testing.T) {
	results := []workspace.ToolResult{{
		Tool:    "discovery_query",
		Summary: "Discovery query complete",
		Output:  `{"artifact_path":".codex/discovery/impact-analysis.json","evidence":[{"path":"internal/agent/runner.go","excerpt":"task child delegated_read_only"},{"path":"internal/agent/runner_child_tasks.go","excerpt":"child task runner"}]}`,
	}}
	runner := &Runner{
		AgentName: "child:explorer",
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "delegated_read_only",
			Objective:         "Explain child discovery.",
			PermissionProfile: "read_only_workspace",
		},
	}

	ready := runner.delegatedReadOnlyAnswerReadyAfterResult(
		workspace.ToolRequest{Tool: "discovery_query"},
		results[0],
		results,
	)
	if !ready {
		t.Fatalf("expected sufficient discovery_query packet to mark delegated read-only evidence as answer-ready")
	}

	mutationRunner := *runner
	mutationRunner.RuntimeBrief.Contract = "implementation"
	if mutationRunner.delegatedReadOnlyAnswerReadyAfterResult(workspace.ToolRequest{Tool: "discovery_query"}, results[0], results) {
		t.Fatalf("did not expect answer-ready shortcut for mutation-required child")
	}

	failingResult := results[0]
	failingResult.Error = "discovery failed"
	if runner.delegatedReadOnlyAnswerReadyAfterResult(workspace.ToolRequest{Tool: "discovery_query"}, failingResult, []workspace.ToolResult{failingResult}) {
		t.Fatalf("did not expect answer-ready shortcut after failed discovery")
	}
}

func TestReadOnlyAnalysisAnswerReadyAfterExactArchitectureProbes(t *testing.T) {
	results := []workspace.ToolResult{
		{
			Tool:    "discovery_query",
			Summary: "Ran architecture_boundary discovery query",
			Output:  `{"artifact_path":".idiom/discovery/queries/query.json","evidence":[{"name":"text_anchor_search","summary":"Found subagent flow.","output_excerpt":"internal/agent/runner_child_tasks.go:16 runChildTask\ninternal/workspace/tool_schemas.go:85 task"}]}`,
		},
		{
			Tool:    "go_read_symbol",
			Summary: "Read Runner.runChildTask in internal/agent/runner_child_tasks.go lines 16-132",
			Output:  "func (r *Runner) runChildTask(...) {}",
		},
		{
			Tool:    "go_read_symbol",
			Summary: "Read Runner.runWorkbranchChildTask in internal/agent/runner_child_tasks.go lines 326-422",
			Output:  "func (r *Runner) runWorkbranchChildTask(...) {}",
		},
	}
	runner := &Runner{
		AgentName: "main",
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "architecture",
			Contract:          "architecture_assessment",
			Objective:         "Explain how this harness uses sub-agents.",
			PermissionProfile: "read_only_workspace",
		},
	}
	if !runner.readOnlyAnalysisAnswerReadyAfterResult(workspace.ToolRequest{Tool: "go_read_symbol"}, results[2], results) {
		t.Fatalf("expected architecture lane to become answer-ready after exact semantic probes")
	}
}

func TestEmitDelegatedReadOnlyAnswerReadyEventCanonicalizesEvidencePaths(t *testing.T) {
	var events []RunEvent
	runner := &Runner{
		AgentName: "child:explorer",
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: "delegated_read_only",
			Objective:  "Explain how this harness uses subagents.",
		},
		OnEvent: func(event RunEvent) {
			events = append(events, event)
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "discovery_query",
		Summary: "Discovery query complete",
		Output:  `{"artifact_path":".codex/discovery/impact-analysis.json","evidence":[{"name":"go_read_symbol","summary":"Read subagent flow","output_excerpt":"runner.go:648 runChildTask\ninternal/agent/runner_child_tasks.go:16 runChildTask\ninternal/agent/answer_composer.go:73 synthesizeDelegatedReadOnlyAnswer"}]}`,
	}}
	runner.emitDelegatedReadOnlyAnswerReadyEvent(workspace.ToolRequest{Tool: "discovery_query"}, results)
	if len(events) != 1 {
		t.Fatalf("expected one answer-ready event, got %d", len(events))
	}
	var payload struct {
		EvidencePaths []string `json:"evidence_paths"`
	}
	if err := json.Unmarshal([]byte(events[0].Content), &payload); err != nil {
		t.Fatalf("unmarshal event payload: %v", err)
	}
	for _, want := range []string{
		"internal/agent/runner.go",
		"internal/agent/runner_child_tasks.go",
		"internal/agent/answer_composer.go",
	} {
		if !containsRunnerString(payload.EvidencePaths, want) {
			t.Fatalf("expected answer-ready event to include %q, got %#v", want, payload.EvidencePaths)
		}
	}
	for _, forbidden := range []string{"runner.go", "runner_child_tasks.go"} {
		if containsRunnerString(payload.EvidencePaths, forbidden) {
			t.Fatalf("expected answer-ready event to avoid bare duplicate path %q, got %#v", forbidden, payload.EvidencePaths)
		}
	}
}

func TestPostReadinessGapScopeRequiresNamedGapForChildReadFollowup(t *testing.T) {
	scope := postReadinessGapScope(
		"child:explorer",
		true,
		false,
		workspace.ToolRequest{Tool: "read_file", Path: "internal/agent/runner.go"},
	)
	if scope != "delegated_read_only_answer_ready" {
		t.Fatalf("unexpected scope %q", scope)
	}
	if gap := postReadinessGap(workspace.ToolRequest{}); gap != "" {
		t.Fatalf("expected empty gap, got %q", gap)
	}
	if gap := postReadinessGap(workspace.ToolRequest{NamedGap: "inspect the exact exit branch"}); gap != "inspect the exact exit branch" {
		t.Fatalf("unexpected named gap %q", gap)
	}
}

func TestPostReadinessGapScopeRequiresNamedGapAfterChildPacket(t *testing.T) {
	scope := postReadinessGapScope(
		"architecture",
		false,
		true,
		workspace.ToolRequest{Tool: "search_content", Pattern: "runner", Path: "internal"},
	)
	if scope != "child_packet_available" {
		t.Fatalf("unexpected scope %q", scope)
	}
	summary, errorText := missingPostReadinessGapMessage(scope)
	if summary != "Post-child follow-up missing named gap" {
		t.Fatalf("unexpected summary %q", summary)
	}
	for _, want := range []string{"`named_gap`", "`why_not_answering`", "discovered handle", "raw path request"} {
		if !strings.Contains(errorText, want) {
			t.Fatalf("expected named-gap guidance to contain %q, got %q", want, errorText)
		}
	}
}

func TestInvalidPostReadinessGapRejectsUnanchoredPostChildSearch(t *testing.T) {
	results := []workspace.ToolResult{{
		Tool:   "task",
		Output: "Question: explain subagents\nSource: delegated read-only evidence packet.\n\nInspected paths:\n- `internal/agent/runner.go`\n- `internal/agent/answer_composer.go`\n",
	}}
	summary, errorText, rejected := invalidPostReadinessGap("child_packet_available", "Explain how this harness uses subagents.", workspace.ToolRequest{
		Tool:            "search_files",
		Pattern:         "*harness*",
		NamedGap:        "Locate the exact file that defines the harness handoff",
		WhyNotAnswering: "I need one packet-anchored file before answering",
	}, results)
	if !rejected {
		t.Fatalf("expected unanchored post-child search to be rejected")
	}
	if summary != "Post-child follow-up gap is not packet-anchored" {
		t.Fatalf("unexpected summary %q", summary)
	}
	for _, want := range []string{"packet already surfaced relevant source paths", "packet-named path", "repo-wide filename or content search"} {
		if !strings.Contains(errorText, want) {
			t.Fatalf("expected rejection text to contain %q, got %q", want, errorText)
		}
	}
}

func TestInvalidPostReadinessGapRejectsContradictedGap(t *testing.T) {
	results := []workspace.ToolResult{{
		Tool:   "task",
		Output: "Question: explain subagents\nSource: delegated read-only evidence packet.\n\nInspected paths:\n- `internal/agent/runner.go`\n- `internal/agent/answer_composer.go`\n",
	}}
	summary, errorText, rejected := invalidPostReadinessGap("child_packet_available", "Explain how this harness uses subagents.", workspace.ToolRequest{
		Tool:            "search_content",
		Pattern:         "harness|sub.?agent",
		NamedGap:        "Locate the harness code and how it invokes sub-agents",
		WhyNotAnswering: "I have not yet seen any harness code or sub-agent invocation patterns; the child task output was incomplete and I need to find the actual source files",
	}, results)
	if !rejected {
		t.Fatalf("expected contradicted post-child gap to be rejected")
	}
	if summary != "Post-child follow-up gap is contradicted by current evidence" {
		t.Fatalf("unexpected summary %q", summary)
	}
	for _, want := range []string{"already surfaced grounded source evidence", "Do not claim that source files or harness code are still unseen", "answer now from the child packet"} {
		if !strings.Contains(errorText, want) {
			t.Fatalf("expected contradiction text to contain %q, got %q", want, errorText)
		}
	}
}

func TestInvalidPostReadinessGapAllowsPacketAnchoredRead(t *testing.T) {
	results := []workspace.ToolResult{{
		Tool:   "task",
		Output: "Question: explain subagents\nSource: delegated read-only evidence packet.\n\nInspected paths:\n- `internal/agent/answer_composer.go`\n",
	}}
	_, _, rejected := invalidPostReadinessGap("child_packet_available", "Explain how this harness uses subagents.", workspace.ToolRequest{
		Tool:            "read_file",
		Path:            "internal/agent/answer_composer.go",
		StartLine:       60,
		EndLine:         120,
		NamedGap:        "Read the explanation body that composes the subagent answer",
		WhyNotAnswering: "I need one anchored range from the packet-named file before answering",
	}, results)
	if rejected {
		t.Fatalf("did not expect packet-anchored child follow-up to be rejected")
	}
}

func TestInvalidPostReadinessGapRejectsPromptArtifactPath(t *testing.T) {
	results := []workspace.ToolResult{{
		Tool:   "task",
		Output: "Question: explain subagents\nSource: delegated read-only evidence packet.\n\nInspected paths:\n- `internal/agent/answer_composer.go`\n",
	}}
	summary, _, rejected := invalidPostReadinessGap("child_packet_available", "Explain how this harness uses subagents.", workspace.ToolRequest{
		Tool:            "read_file",
		Path:            ".idiom/discovery/context.md",
		NamedGap:        "Read the generated context for more harness details",
		WhyNotAnswering: "I need one more source of context before answering",
	}, results)
	if !rejected {
		t.Fatalf("expected prompt artifact path to be rejected")
	}
	if summary != "Post-child follow-up gap is not packet-anchored" {
		t.Fatalf("unexpected summary %q", summary)
	}
}

func TestInvalidPostReadinessGapRejectsBroadChildFileTopicRead(t *testing.T) {
	results := []workspace.ToolResult{
		{
			Tool:    "discovery_query",
			Summary: "Ran architecture_boundary discovery query",
			Output:  `{"artifact_path":".idiom/discovery/queries/query.json","evidence":[{"name":"go_read_symbol","summary":"Read subagent flow","output_excerpt":"internal/agent/runner.go:648 runChildTask\ninternal/agent/runner_child_tasks.go:16 runChildTask"}]}`,
		},
	}
	summary, errorText, rejected := invalidPostReadinessGap("delegated_read_only_answer_ready", "Explain how this harness uses subagents.", workspace.ToolRequest{
		Tool:      "read_file",
		Path:      "internal/agent/runner_child_tasks.go",
		StartLine: 1,
		EndLine:   100,
		NamedGap:  "How runner_child_tasks.go implements sub-agent delegation via the task tool",
	}, results)
	if !rejected {
		t.Fatalf("expected broad child file-topic read to be rejected")
	}
	if summary != "Post-readiness follow-up is not an exact probe" {
		t.Fatalf("unexpected summary %q", summary)
	}
	for _, want := range []string{"file-level raw read is too broad", "exact branch, function body, symbol", "answer now"} {
		if !strings.Contains(errorText, want) {
			t.Fatalf("expected broad-read rejection to contain %q, got %q", want, errorText)
		}
	}
	if !strings.Contains(errorText, "`runChildTask`") {
		t.Fatalf("expected broad-read rejection to contain evidence-grounded probe suggestion, got %q", errorText)
	}
}

func TestInvalidPostReadinessGapRejectsFileOutlineAfterReadiness(t *testing.T) {
	results := []workspace.ToolResult{{
		Tool:   "task",
		Output: "Question: explain subagents\nSource: delegated read-only evidence packet.\n\nInspected paths:\n- `internal/agent/runner_child_tasks.go`\n- `internal/agent/runner.go`\n",
	}}
	summary, errorText, rejected := invalidPostReadinessGap("child_packet_available", "Explain how this harness uses subagents.", workspace.ToolRequest{
		Tool:            "go_file_outline",
		Path:            "internal/agent/runner_child_tasks.go",
		NamedGap:        "runner_child_tasks.go should contain the child task delegation entrypoint",
		WhyNotAnswering: "I need to identify one exact body before answering",
	}, results)
	if !rejected {
		t.Fatalf("expected post-readiness file outline to be rejected")
	}
	if summary != "Post-readiness follow-up is broader than the current evidence requires" {
		t.Fatalf("unexpected summary %q", summary)
	}
	for _, want := range []string{"file-level outline is broad orientation", "exact symbol/body/range", "answer now"} {
		if !strings.Contains(errorText, want) {
			t.Fatalf("expected outline rejection to contain %q, got %q", want, errorText)
		}
	}
}

func TestInvalidPostReadinessGapRejectsBroadSearchAfterReadiness(t *testing.T) {
	results := []workspace.ToolResult{{
		Tool:   "discovery_query",
		Output: `{"artifact_path":".idiom/discovery/queries/query.json","evidence":[{"name":"text_anchor_search","summary":"Found implementation paths.","output_excerpt":"internal/agent/runner_child_tasks.go:16: func (r *Runner) runChildTask(ctx context.Context, req workspace.ToolRequest, opts llama.ChatOptions) workspace.ToolResult"}]}`,
	}}
	summary, errorText, rejected := invalidPostReadinessGap("delegated_read_only_answer_ready", "Explain how this harness uses subagents.", workspace.ToolRequest{
		Tool:            "search_content",
		Path:            "internal/agent",
		Pattern:         `func.*[Cc]hild|[Ss]ub[Aa]gent|[Dd]elegat`,
		NamedGap:        "how runner_child_tasks.go implements sub-agent delegation",
		WhyNotAnswering: "need to understand the child task/sub-agent mechanism before answering",
	}, results)
	if !rejected {
		t.Fatalf("expected post-readiness broad search to be rejected")
	}
	if summary != "Post-readiness search is broader than the current evidence requires" {
		t.Fatalf("unexpected summary %q", summary)
	}
	for _, want := range []string{"search_content", "broad rediscovery pattern", "exact probe", "answer now"} {
		if !strings.Contains(errorText, want) {
			t.Fatalf("expected broad-search rejection to contain %q, got %q", want, errorText)
		}
	}
}

func TestInvalidPostReadinessGapAllowsExactSearchAfterReadiness(t *testing.T) {
	results := []workspace.ToolResult{{
		Tool:   "discovery_query",
		Output: `{"artifact_path":".idiom/discovery/queries/query.json","evidence":[{"name":"text_anchor_search","summary":"Found implementation paths.","output_excerpt":"internal/agent/runner_child_tasks.go:16: func (r *Runner) runChildTask(ctx context.Context, req workspace.ToolRequest, opts llama.ChatOptions) workspace.ToolResult"}]}`,
	}}
	_, _, rejected := invalidPostReadinessGap("delegated_read_only_answer_ready", "Explain how this harness uses subagents.", workspace.ToolRequest{
		Tool:            "search_content",
		Path:            "internal/agent/runner_child_tasks.go",
		Pattern:         "runChildTask",
		NamedGap:        "the body of `runChildTask` determines child task spawning",
		WhyNotAnswering: "I need one exact symbol anchor before answering",
	}, results)
	if rejected {
		t.Fatalf("did not expect exact post-readiness search to be rejected")
	}
}

func TestAugmentDelegatedReadOnlyPacketPreservesAdditionalPaths(t *testing.T) {
	answer := "Question: Explain how subagents work.\nSource: delegated read-only evidence packet.\n\nInspected paths:\n- `internal/agent/runner_child_tasks.go`\n"
	results := []workspace.ToolResult{
		{
			Tool:    "discovery_query",
			Summary: "Ran architecture_boundary discovery query",
			Output:  `{"artifact_path":".idiom/discovery/queries/query.json","evidence":[{"name":"go_read_symbol","summary":"Read subagent flow","output_excerpt":"internal/agent/runner.go:648 runChildTask\ninternal/agent/runner_child_tasks.go:16 runChildTask\ninternal/agent/answer_composer.go:18 Compose"}]}`,
		},
	}
	augmented := augmentDelegatedReadOnlyPacket(answer, "Explain how subagents work.", results)
	for _, want := range []string{
		"Additional inspected paths:",
		"`internal/agent/runner.go`",
		"`internal/agent/answer_composer.go`",
		"Exact probes already inspected:",
		"`runChildTask`",
	} {
		if !strings.Contains(augmented, want) {
			t.Fatalf("expected augmented delegated packet to contain %q, got %q", want, augmented)
		}
	}
}

func TestInvalidPostReadinessGapDoesNotUseQuestionClassSpecificAdjacentDetailRule(t *testing.T) {
	results := []workspace.ToolResult{
		{
			Tool:   "task",
			Output: "Question: explain subagents\nSource: delegated read-only evidence packet.\n\nInspected paths:\n- `internal/agent/runner.go`\n- `internal/agent/runner_child_tasks.go`\n\nExact probes already inspected:\n- `runChildTask`\n",
		},
		{
			Tool:    "go_read_symbol",
			Summary: "Read Runner.runChildTask in internal/agent/runner_child_tasks.go lines 16-131",
			Output:  "func (r *Runner) runChildTask(...) { /* child handoff */ }",
		},
	}
	summary, errorText, rejected := invalidPostReadinessGap("child_packet_available", "Explain how this harness uses subagents.", workspace.ToolRequest{
		Tool:            "go_read_symbol",
		Path:            "internal/agent/runner_child_tasks.go",
		Pattern:         "workbranchChildTaskPermissionPolicy",
		NamedGap:        "What tools are allowed/denied for read-only child tasks vs workbranch child tasks?",
		WhyNotAnswering: "Need the permission-policy comparison before I can answer how the harness uses subagents",
	}, results)
	if rejected {
		t.Fatalf("did not expect anchored exact probe to be rejected by a question-class-specific adjacent-detail rule; summary=%q error=%q", summary, errorText)
	}
}

func TestEmitAcceptedAnswerSignalsSanitizesEvidencePaths(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Lane:       "architecture",
			Contract:   "architecture_assessment",
			TaskFamily: "architecture",
			Objective:  "Explain how this harness uses subagents.",
		},
	}
	var readiness map[string]any
	runner.OnEvent = func(event RunEvent) {
		if event.Role != "readiness_check" {
			return
		}
		_ = json.Unmarshal([]byte(event.Content), &readiness)
	}
	runner.emitAcceptedAnswerSignals(
		"The harness delegates through `internal/agent/runner.go` and `internal/agent/runner_child_tasks.go` rather than `symbol/body/branch` markers or `.idiom/discovery/queries/query.json` artifacts.",
		[]workspace.ToolResult{{
			Tool:    "task",
			Summary: "Child task completed",
			Output:  "Question: explain subagents\nSource: delegated read-only evidence packet.\n\nInspected paths:\n- `internal/agent/runner.go`\n- `.idiom/discovery/queries/query.json`\n- `internal/agent/runner_child_tasks.go`\n",
		}},
	)
	rawPaths, _ := readiness["evidence_paths"].([]any)
	got := make([]string, 0, len(rawPaths))
	for _, raw := range rawPaths {
		if value, ok := raw.(string); ok {
			got = append(got, value)
		}
	}
	for _, want := range []string{"internal/agent/runner.go", "internal/agent/runner_child_tasks.go"} {
		if !containsRunnerString(got, want) {
			t.Fatalf("expected sanitized readiness paths to contain %q, got %#v", want, got)
		}
	}
	for _, forbidden := range []string{".idiom/discovery/queries/query.json", "symbol/body/branch"} {
		if containsRunnerString(got, forbidden) {
			t.Fatalf("did not expect sanitized readiness paths to retain %q, got %#v", forbidden, got)
		}
	}
}

func TestEmitAcceptedAnswerSignalsPrefersExactProbePathsOverNoisyPinnedPaths(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Lane:              "architecture",
			Contract:          "architecture_assessment",
			TaskFamily:        "architecture",
			Objective:         "Explain how this harness uses subagents.",
			PermissionProfile: "read_only_workspace",
		},
	}
	var readiness map[string]any
	runner.OnEvent = func(event RunEvent) {
		if event.Role != "readiness_check" {
			return
		}
		_ = json.Unmarshal([]byte(event.Content), &readiness)
	}
	runner.emitAcceptedAnswerSignals(
		"`Runner.runChildTask` creates the bounded child runner in `internal/agent/runner_child_tasks.go` and returns the child result to the parent as evidence.",
		[]workspace.ToolResult{
			{
				Tool:    "task",
				Summary: "Child task completed",
				Output:  "Primary files: `internal/agent/instructions.go`, `internal/agent/modes.go`.",
			},
			{
				Tool:    "go_read_symbol",
				Summary: "Read Runner.runChildTask in internal/agent/runner_child_tasks.go lines 16-132",
				Output:  "func (r *Runner) runChildTask(...) workspace.ToolResult { return workspace.ToolResult{} }",
			},
		},
	)
	rawPaths, _ := readiness["evidence_paths"].([]any)
	got := make([]string, 0, len(rawPaths))
	for _, raw := range rawPaths {
		if value, ok := raw.(string); ok {
			got = append(got, value)
		}
	}
	if !containsRunnerString(got, "internal/agent/runner_child_tasks.go") {
		t.Fatalf("expected exact probe path in readiness paths, got %#v", got)
	}
	for _, forbidden := range []string{"internal/agent/instructions.go", "internal/agent/modes.go"} {
		if containsRunnerString(got, forbidden) {
			t.Fatalf("did not expect noisy pinned path %q in readiness paths, got %#v", forbidden, got)
		}
	}
}

func TestReadOnlyExplanationCompletionRejectionRejectsMetaScopeSummary(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Contract:          "architecture_assessment",
			TaskFamily:        "architecture",
			PermissionProfile: "read_only_workspace",
		},
	}
	result, rejected := runner.completionGateRejection(
		"I have sufficient evidence to answer. The strongest implementation evidence is in `internal/agent/runner.go`.",
		nil,
		1,
		nil,
		false,
	)
	if !rejected {
		t.Fatalf("expected meta scope summary to be rejected")
	}
	if !strings.Contains(result.Error, "implementation surface") && !strings.Contains(result.Error, "concrete behavior") {
		t.Fatalf("expected explanatory rejection, got %#v", result)
	}
}

func TestReadOnlyExplanationCompletionRejectionRejectsUnsupportedExtraDetail(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Contract:          "architecture_assessment",
			TaskFamily:        "architecture",
			PermissionProfile: "read_only_workspace",
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "go_read_symbol",
		Summary: "Read Runner.runChildTask in internal/agent/runner_child_tasks.go lines 16-132",
		Output:  "func (r *Runner) runChildTask(...) workspace.ToolResult { return workspace.ToolResult{} }",
	}}
	result, rejected := runner.completionGateRejection(
		"`Runner.runChildTask` creates the read-only child runner, and `Runner.runWorkbranchChildTask` applies the write-enabled variant.",
		results,
		1,
		nil,
		false,
	)
	if !rejected {
		t.Fatalf("expected unsupported extra symbol detail to be rejected")
	}
	if !strings.Contains(result.Error, "runWorkbranchChildTask") {
		t.Fatalf("expected rejection to name unsupported detail, got %#v", result)
	}
}

func TestClosurePromptEvidencePathsDropsArtifacts(t *testing.T) {
	got := closurePromptEvidencePaths([]string{
		".idiom/discovery/queries/query.json",
		"start_line/end_line",
		"search/read",
		"internal/agent/runner.go",
		"internal/agent/runner_child_tasks.go:93-94",
		"idiom/internal/frontier",
	}, 0)
	want := []string{
		"internal/agent/runner.go",
		"internal/agent/runner_child_tasks.go",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected prompt evidence paths %#v, got %#v", want, got)
	}
}

func TestPromptEvidencePathsFromToolResultsDropsPromptArtifacts(t *testing.T) {
	got := promptEvidencePathsFromToolResults([]workspace.ToolResult{{
		Tool:    "task",
		Summary: "Child task completed",
		Output:  "Inspected paths:\n- `.idiom/discovery/queries/query.json`\n- `internal/agent/runner.go`\n- `internal/agent/runner_child_tasks.go:93-94`\n- `search/read`\n",
	}}, 6)
	want := []string{
		"internal/agent/runner.go",
		"internal/agent/runner_child_tasks.go",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected sanitized prompt evidence paths %#v, got %#v", want, got)
	}
}

func TestPromptEvidencePathsFromToolResultsPrefersExactProbePathsOverEarlyScaffolding(t *testing.T) {
	got := promptEvidencePathsFromToolResults([]workspace.ToolResult{
		{
			Tool:   "discovery_query",
			Output: "codex/README.md\ncodex/project-context.md\ncodex/AGENTS.md\ninternal/agent/runner_child_tasks.go\ninternal/workspace/tool_schemas.go\n",
		},
		{
			Tool:    "go_read_symbol",
			Summary: "Read Runner.runChildTask in internal/agent/runner_child_tasks.go lines 16-132",
			Output:  "func (r *Runner) runChildTask(...) {}",
		},
		{
			Tool:    "go_read_symbol",
			Summary: "Read Runner.runWorkbranchChildTask in internal/agent/runner_child_tasks.go lines 326-422",
			Output:  "func (r *Runner) runWorkbranchChildTask(...) {}",
		},
		{
			Tool:    "search_content",
			Summary: "Found 1 matching line at internal/workspace/tool_schemas.go:85.",
			Output:  "internal/workspace/tool_schemas.go:85: Delegate a bounded evidence-gathering question to a sub-agent.",
		},
	}, 4)
	if len(got) == 0 || got[0] != "internal/agent/runner_child_tasks.go" {
		t.Fatalf("expected exact semantic probe path to be prioritized, got %#v", got)
	}
	for _, forbidden := range []string{"codex/README.md", "codex/project-context.md", "codex/AGENTS.md"} {
		if containsRunnerString(got, forbidden) {
			t.Fatalf("expected prompt scaffolding path %q to be deprioritized, got %#v", forbidden, got)
		}
	}
}

func TestPostChildClosureEvidencePathsPreservesMultiFilePacketCoverage(t *testing.T) {
	got := postChildClosureEvidencePaths("Explain how this harness uses subagents.", []workspace.ToolResult{{
		Tool: "task",
		Output: "Question: explain subagents\nSource: delegated read-only evidence packet.\n\nInspected paths:\n" +
			"- `.idiom/discovery/queries/query.json`\n" +
			"- `runner.go`\n" +
			"- `internal/agent/runner.go`\n" +
			"- `internal/agent/runner_child_tasks.go:93-94`\n" +
			"- `internal/agent/answer_composer.go`\n" +
			"- `search/read`\n",
	}}, 6)
	want := []string{
		"internal/agent/runner.go",
		"internal/agent/runner_child_tasks.go",
		"internal/agent/answer_composer.go",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected post-child evidence paths %#v, got %#v", want, got)
	}
}

func TestDelegatedClosureEvidencePathsRetainsDiscoveryPacketAfterExactProbe(t *testing.T) {
	got := delegatedClosureEvidencePaths("How does this harness utilize sub-agents?", []workspace.ToolResult{
		{
			Tool:    "discovery_query",
			Summary: "Ran function_tracing discovery query",
			Output:  `{"artifact_path":".idiom/discovery/queries/query.json","evidence":[{"name":"go_read_symbol","summary":"Read flow","output_excerpt":"internal/agent/runner.go:648 runChildTask\ninternal/agent/runner_child_tasks.go:16 runChildTask\ninternal/agent/answer_composer.go:18 Compose\ninternal/agent/runner_test.go:1 package agent"}]}`,
		},
		{
			Tool:    "go_read_symbol",
			Summary: "Read Runner.runChildTask in internal/agent/runner_child_tasks.go lines 16-131",
			Output:  "func (r *Runner) runChildTask(...) {}",
		},
	}, 6)
	want := []string{
		"internal/agent/runner_child_tasks.go",
		"internal/agent/runner.go",
		"internal/agent/answer_composer.go",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected delegated closure evidence paths %#v, got %#v", want, got)
	}
}

func TestClosurePromptEvidencePathsPrefersImplementationOverTests(t *testing.T) {
	got := closurePromptEvidencePaths([]string{
		"internal/agent/runner_test.go",
		"internal/agent/runner.go",
		"internal/agent/runner_child_tasks.go",
	}, 0)
	want := []string{
		"internal/agent/runner.go",
		"internal/agent/runner_child_tasks.go",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected non-test closure evidence paths %#v, got %#v", want, got)
	}
}

func TestInvalidPostReadinessGapRejectsOutlineWhenExactProbeAlreadyKnown(t *testing.T) {
	results := []workspace.ToolResult{
		{
			Tool:    "discovery_query",
			Summary: "Ran architecture_boundary discovery query",
			Output:  `{"artifact_path":".idiom/discovery/queries/query.json","evidence":[{"name":"go_read_symbol","summary":"Read flow","output_excerpt":"internal/agent/runner_child_tasks.go:16 runChildTask"}]}`,
		},
	}
	summary, errorText, rejected := invalidPostReadinessGap("delegated_read_only_answer_ready", "Explain the child runner flow.", workspace.ToolRequest{
		Tool:            "go_file_outline",
		Path:            "internal/agent/runner_child_tasks.go",
		NamedGap:        "runner_child_tasks.go:16 runChildTask body should show the child runner flow",
		WhyNotAnswering: "I need one exact body before answering",
	}, results)
	if !rejected {
		t.Fatalf("expected file outline to be rejected once an exact probe is already known")
	}
	if summary != "Post-readiness follow-up is broader than the current evidence requires" {
		t.Fatalf("unexpected summary %q", summary)
	}
	for _, want := range []string{"file-level outline is broad orientation", "`runChildTask`", "evidence already gathered"} {
		if !strings.Contains(errorText, want) {
			t.Fatalf("expected outline rejection to contain %q, got %q", want, errorText)
		}
	}
}

func TestInvalidPostReadinessGapRejectsWholeFileReadWhenExactProbeAlreadyKnown(t *testing.T) {
	results := []workspace.ToolResult{
		{
			Tool:    "discovery_query",
			Summary: "Ran function_tracing discovery query",
			Output:  `{"artifact_path":".idiom/discovery/queries/query.json","evidence":[{"name":"go_read_symbol","summary":"Read flow","output_excerpt":"internal/agent/runner_child_tasks.go:16 runChildTask"}]}`,
		},
	}
	summary, errorText, rejected := invalidPostReadinessGap("delegated_read_only_answer_ready", "Explain the child runner flow.", workspace.ToolRequest{
		Tool:            "read_file",
		Path:            "internal/agent/runner_child_tasks.go",
		NamedGap:        "How runChildTask constructs and executes sub-agent tasks with delegated instructions",
		WhyNotAnswering: "I have not yet inspected the exact implementation body",
	}, results)
	if !rejected {
		t.Fatalf("expected raw whole-file reread to be rejected once an exact probe is already known")
	}
	if summary != "Post-readiness follow-up is not an exact probe" {
		t.Fatalf("unexpected summary %q", summary)
	}
	for _, want := range []string{"file-level raw read is too broad", "Known exact probes for this path", "`runChildTask`"} {
		if !strings.Contains(errorText, want) {
			t.Fatalf("expected raw-read rejection to contain %q, got %q", want, errorText)
		}
	}
}

func TestInvalidPostReadinessGapRejectsUngroundedExactProbe(t *testing.T) {
	results := []workspace.ToolResult{
		{
			Tool:    "discovery_query",
			Summary: "Ran architecture_boundary discovery query",
			Output:  `{"artifact_path":".idiom/discovery/queries/query.json","evidence":[{"name":"go_read_symbol","summary":"Read flow","output_excerpt":"internal/agent/runner_child_tasks.go:16 runChildTask"}]}`,
		},
	}
	summary, errorText, rejected := invalidPostReadinessGap("delegated_read_only_answer_ready", "Explain the child runner flow.", workspace.ToolRequest{
		Tool:            "go_extract_body",
		Path:            "internal/agent/runner_child_tasks.go",
		Pattern:         "delegatedReadOnlyLikelySourceCandidates",
		NamedGap:        "Need to inspect delegatedReadOnlyLikelySourceCandidates before answering",
		WhyNotAnswering: "I have not yet seen the exact body I need",
	}, results)
	if !rejected {
		t.Fatalf("expected ungrounded exact probe to be rejected")
	}
	if summary != "Post-readiness exact probe is not grounded in current evidence" {
		t.Fatalf("unexpected summary %q", summary)
	}
	for _, want := range []string{"not grounded in the current evidence", "`runChildTask`", "Use one evidence-grounded exact probe or answer now"} {
		if !strings.Contains(errorText, want) {
			t.Fatalf("expected exact-probe rejection to contain %q, got %q", want, errorText)
		}
	}
}

func TestClosureDecisionRationaleUsesActualPacketCoverage(t *testing.T) {
	results := []workspace.ToolResult{{
		Tool:   "task",
		Output: "Question: explain subagents\nSource: delegated read-only evidence packet.\n\nInspected paths:\n- `internal/agent/runner_child_tasks.go`\n",
	}}
	validity, diminishing := closureDecisionRationale("Explain how this harness uses subagents.", results, nil)
	if !strings.Contains(validity, "internal/agent/runner_child_tasks.go") {
		t.Fatalf("expected validity text to cite actual packet path, got %q", validity)
	}
	for _, forbidden := range []string{"handoff classifier", "answer-composer path"} {
		if strings.Contains(validity, forbidden) {
			t.Fatalf("did not expect broad subagent coverage claim %q in %q", forbidden, validity)
		}
	}
	if !strings.Contains(diminishing, "named gap") {
		t.Fatalf("expected diminishing-returns text to stay gap-shaped, got %q", diminishing)
	}
}

func TestBuildClosureRecoveryNudgeAvoidsRepeatedReadSuggestion(t *testing.T) {
	results := []workspace.ToolResult{
		{
			Tool:   "task",
			Output: "Question: explain subagents\nSource: delegated read-only evidence packet.\n\nInspected paths:\n- `internal/agent/runner_child_tasks.go`\n",
		},
		{
			Tool:    "read_file",
			Summary: "Repeated dead-end action rejected",
			Error:   "this action class already failed earlier in the turn. Do not retry the same kind of move. Choose a materially different step or answer with the evidence you already have.",
		},
	}
	nudge := buildClosureRecoveryNudge("child_packet_available", workspace.ToolRequest{
		Tool:            "read_file",
		Path:            "internal/agent/runner_child_tasks.go",
		NamedGap:        "How runChildTask spawns the child runner with bounded tool budget and collects results",
		WhyNotAnswering: "Without the body of runChildTask I cannot explain the sub-agent lifecycle",
	}, results, workerruntime.WorkerRuntimeBrief{TaskFamily: "architecture", Objective: "Explain how this harness uses subagents."})
	for _, forbidden := range []string{`{"tool":"read_file"`, `{"tool":"go_file_outline"`} {
		if strings.Contains(nudge, forbidden) {
			t.Fatalf("expected closure recovery nudge to avoid concrete probe JSON %q, got:\n%s", forbidden, nudge)
		}
	}
	for _, want := range []string{"Grounded evidence paths:", "internal/agent/runner_child_tasks.go", "Answer now from the evidence already gathered"} {
		if !strings.Contains(nudge, want) {
			t.Fatalf("expected recovery nudge to contain %q, got:\n%s", want, nudge)
		}
	}
}

func TestBuildClosureRecoveryNudgeKeepsCorrectedReadAfterMissingGap(t *testing.T) {
	results := []workspace.ToolResult{
		{
			Tool:   "task",
			Output: "Question: explain subagents\nSource: delegated read-only evidence packet.\n\nInspected paths:\n- `internal/agent/runner_child_tasks.go`\n",
		},
		{
			Tool:    "read_file",
			Summary: "Post-child follow-up missing named gap",
			Error:   "the child packet already contains sufficient evidence to bias toward answering. Before any additional read/search call, include `named_gap` describing one exact unresolved claim this follow-up will verify or falsify.",
		},
	}
	nudge := buildClosureRecoveryNudge("child_packet_available", workspace.ToolRequest{
		Tool: "read_file",
		Path: "internal/agent/runner_child_tasks.go",
	}, results, workerruntime.WorkerRuntimeBrief{TaskFamily: "architecture", Objective: "Explain how this harness uses subagents."})
	if strings.Contains(nudge, `{"tool":"read_file"`) {
		t.Fatalf("expected closure recovery nudge to avoid concrete read_file JSON after missing-gap repair, got:\n%s", nudge)
	}
	for _, want := range []string{"Grounded evidence paths:", "internal/agent/runner_child_tasks.go", "Answer now from the evidence already gathered"} {
		if !strings.Contains(nudge, want) {
			t.Fatalf("expected recovery nudge to contain %q, got:\n%s", want, nudge)
		}
	}
}

func TestDelegatedReadOnlyDiminishingReturnsNudgeEncouragesExactProbe(t *testing.T) {
	results := []workspace.ToolResult{{
		Tool:    "discovery_query",
		Summary: "Ran architecture_boundary discovery query",
		Output:  `{"artifact_path":".idiom/discovery/queries/query.json","evidence":[{"path":"internal/agent/runner.go","excerpt":"delegatedDiscoveryAnswerEvidencePaths"},{"path":"internal/agent/runner_child_tasks.go","excerpt":"runChildTask"}]}`,
	}}
	first := delegatedReadOnlyDiminishingReturnsNudge("Explain how this harness uses subagents.", results, 1)
	second := delegatedReadOnlyDiminishingReturnsNudge("Explain how this harness uses subagents.", results, 2)
	for _, want := range []string{"Post-readiness probes used: 1", "final soft warning", "Inspected paths:"} {
		if !strings.Contains(first, want) {
			t.Fatalf("expected first diminishing-returns nudge to contain %q, got:\n%s", want, first)
		}
	}
	if !strings.Contains(second, "final soft warning") {
		t.Fatalf("expected second diminishing-returns nudge to require an exact claim, got:\n%s", second)
	}
}

func TestPostChildDiminishingReturnsNudgeEncouragesExactProbe(t *testing.T) {
	results := []workspace.ToolResult{{
		Tool:   "task",
		Output: "Question: explain subagents\nSource: delegated read-only evidence packet.\n\nInspected paths:\n- `internal/agent/runner.go`\n- `internal/agent/runner_child_tasks.go`\n",
	}}
	nudge := postChildDiminishingReturnsNudge("Explain how this harness uses subagents.", results, 2)
	for _, want := range []string{"Post-child probes used: 2", "final soft warning", "`internal/agent/runner.go`"} {
		if !strings.Contains(nudge, want) {
			t.Fatalf("expected post-child diminishing-returns nudge to contain %q, got:\n%s", want, nudge)
		}
	}
}

func TestClosureSynthesisAfterIgnoredNudgeForPostChildRead(t *testing.T) {
	results := []workspace.ToolResult{{
		Tool:   "task",
		Output: "Question: explain subagents\nSource: delegated read-only evidence packet.\n\nInspected paths:\n- `internal/agent/runner.go`\n- `internal/agent/runner_child_tasks.go`\n",
	}}
	brief := workerruntime.WorkerRuntimeBrief{
		PermissionProfile: "read_only_workspace",
		Objective:         "Explain how this harness uses subagents.",
	}
	prompt, ok := closureSynthesisAfterIgnoredNudgePrompt(
		"main",
		brief,
		results,
		workspace.ToolRequest{Tool: "read_file", Path: "internal/agent/runner_child_tasks.go"},
		false,
		true,
		0,
		1,
	)
	if !ok {
		t.Fatal("expected post-child read after diminishing nudge to force synthesis")
	}
	for _, want := range []string{"Closure evidence is already sufficient", "Tools are now disabled", "internal/agent/runner.go"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected synthesis prompt to contain %q, got:\n%s", want, prompt)
		}
	}
	if !shouldPreferModelBudgetSynthesis("main", brief, results) {
		t.Fatal("expected parent post-child closure to prefer model synthesis over partial fallback")
	}
}

func TestClosureSynthesisSkipsWhenGapDeclared(t *testing.T) {
	results := []workspace.ToolResult{{
		Tool:   "task",
		Output: "Question: explain subagents\nSource: delegated read-only evidence packet.\n\nInspected paths:\n- `internal/agent/runner.go`\n- `internal/agent/runner_child_tasks.go`\n",
	}}
	brief := workerruntime.WorkerRuntimeBrief{
		PermissionProfile: "read_only_workspace",
		Objective:         "Explain how this harness uses subagents.",
	}
	// Same scenario as TestClosureSynthesisAfterIgnoredNudgeForPostChildRead
	// (post-child packet, diminishing nudge already fired, model is making
	// another read), but this time the request carries a tool-shaped gap —
	// the contract the readiness nudge said to satisfy. Force-closing here
	// would punish the model for following instructions; the trigger must
	// not fire.
	_, ok := closureSynthesisAfterIgnoredNudgePrompt(
		"main",
		brief,
		results,
		workspace.ToolRequest{
			Tool:            "go_read_symbol",
			Path:            "internal/agent/runner_child_tasks.go",
			Pattern:         "runChildTask",
			NamedGap:        "the body of `Runner.runChildTask` to confirm budget plumbing",
			WhyNotAnswering: "without the function body the budget allocation is inferred, not verified",
		},
		false,
		true,
		0,
		1,
	)
	if ok {
		t.Fatal("expected closure_synthesis to skip when the request declares a tool-shaped named gap")
	}
}

func TestSanitizeFallbackResultOutputDropsSearchOutput(t *testing.T) {
	for _, tool := range []string{"search_content", "search_files"} {
		got := sanitizeFallbackResultOutput(workspace.ToolResult{Tool: tool, Output: "internal/agent/runner_test.go:123:\toutput := `{\"a\":1}`"})
		if got != "" {
			t.Fatalf("tool=%s: expected output to be dropped (summary suffices), got %q", tool, got)
		}
	}
}

func TestSanitizeFallbackResultOutputKeepsRichEvidenceTools(t *testing.T) {
	for _, tool := range []string{"read_file", "go_read_symbol", "go_extract_body", "task", "discovery_query"} {
		body := "func Foo() { return }"
		got := sanitizeFallbackResultOutput(workspace.ToolResult{Tool: tool, Output: body})
		if got != body {
			t.Fatalf("tool=%s: expected output to be preserved, got %q", tool, got)
		}
	}
}

func TestApplyPostReadinessSchemaRequirementsMakesGapFieldsRequiredOnReadTools(t *testing.T) {
	tools := []llama.Tool{
		{
			Type: "function",
			Function: llama.ToolFunction{
				Name:       "read_file",
				Parameters: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`),
			},
		},
		{
			Type: "function",
			Function: llama.ToolFunction{
				Name:       "edit_file",
				Parameters: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`),
			},
		},
	}
	out := applyPostReadinessSchemaRequirements(tools)
	if len(out) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(out))
	}
	// read_file: gap fields added to required + properties.
	var readSchema map[string]any
	if err := json.Unmarshal(out[0].Function.Parameters, &readSchema); err != nil {
		t.Fatalf("unmarshal read_file params: %v", err)
	}
	required, _ := readSchema["required"].([]any)
	requiredSet := map[string]bool{}
	for _, v := range required {
		if s, ok := v.(string); ok {
			requiredSet[s] = true
		}
	}
	for _, want := range []string{"path", "named_gap", "why_not_answering", "start_line", "end_line"} {
		if !requiredSet[want] {
			t.Fatalf("expected read_file required to include %q, got %#v", want, required)
		}
	}
	props, _ := readSchema["properties"].(map[string]any)
	for _, want := range []string{"path", "named_gap", "why_not_answering", "start_line", "end_line"} {
		if _, ok := props[want]; !ok {
			t.Fatalf("expected read_file properties to include %q, got %#v", want, props)
		}
	}
	for _, field := range []string{"start_line", "end_line"} {
		prop, _ := props[field].(map[string]any)
		if prop["minimum"] != float64(1) {
			t.Fatalf("expected %s minimum=1, got %#v", field, prop)
		}
	}
	// edit_file: NOT a read-class tool, schema unchanged.
	var editSchema map[string]any
	if err := json.Unmarshal(out[1].Function.Parameters, &editSchema); err != nil {
		t.Fatalf("unmarshal edit_file params: %v", err)
	}
	editRequired, _ := editSchema["required"].([]any)
	if len(editRequired) != 1 {
		t.Fatalf("expected edit_file required to remain ['path'], got %#v", editRequired)
	}
	if s, ok := editRequired[0].(string); !ok || s != "path" {
		t.Fatalf("expected edit_file required to remain ['path'], got %#v", editRequired)
	}
	editProps, _ := editSchema["properties"].(map[string]any)
	if _, leaked := editProps["named_gap"]; leaked {
		t.Fatalf("expected edit_file properties to NOT include named_gap, got %#v", editProps)
	}
}

func TestApplyPostReadinessSchemaRequirementsRequiresReadFilePathAnchor(t *testing.T) {
	tools := []llama.Tool{{
		Type: "function",
		Function: llama.ToolFunction{
			Name:       "read_file",
			Parameters: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"id":{"type":"string"}}}`),
		},
	}}
	out := applyPostReadinessSchemaRequirements(tools)
	var schema map[string]any
	if err := json.Unmarshal(out[0].Function.Parameters, &schema); err != nil {
		t.Fatalf("unmarshal read_file params: %v", err)
	}
	required, _ := schema["required"].([]any)
	requiredSet := map[string]bool{}
	for _, v := range required {
		if s, ok := v.(string); ok {
			requiredSet[s] = true
		}
	}
	for _, want := range []string{"named_gap", "why_not_answering", "start_line", "end_line"} {
		if !requiredSet[want] {
			t.Fatalf("expected read_file required to include %q, got %#v", want, required)
		}
	}
	if !requiredSet["path"] {
		t.Fatalf("expected read_file required to include concrete path anchor, got %#v", required)
	}
	if requiredSet["id"] {
		t.Fatalf("expected transient read_file handle id to remain optional, got %#v", required)
	}
}

func TestApplyPostReadinessSchemaRequirementsExposesLineRangesOnSymbolTools(t *testing.T) {
	tools := []llama.Tool{{
		Type: "function",
		Function: llama.ToolFunction{
			Name:       "go_read_symbol",
			Parameters: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"pattern":{"type":"string"}},"required":["path","pattern"]}`),
		},
	}}
	out := applyPostReadinessSchemaRequirements(tools)
	var schema map[string]any
	if err := json.Unmarshal(out[0].Function.Parameters, &schema); err != nil {
		t.Fatalf("unmarshal go_read_symbol params: %v", err)
	}
	required, _ := schema["required"].([]any)
	requiredSet := map[string]bool{}
	for _, v := range required {
		if s, ok := v.(string); ok {
			requiredSet[s] = true
		}
	}
	for _, want := range []string{"path", "pattern", "named_gap", "why_not_answering"} {
		if !requiredSet[want] {
			t.Fatalf("expected go_read_symbol required to include %q, got %#v", want, required)
		}
	}
	if requiredSet["start_line"] || requiredSet["end_line"] {
		t.Fatalf("expected symbol-tool line ranges to remain optional, got %#v", required)
	}
	props, _ := schema["properties"].(map[string]any)
	for _, field := range []string{"start_line", "end_line"} {
		prop, _ := props[field].(map[string]any)
		if prop == nil {
			t.Fatalf("expected %s property to be present, got %#v", field, props)
		}
		if prop["minimum"] != float64(1) {
			t.Fatalf("expected %s minimum=1, got %#v", field, prop)
		}
	}
}

func TestApplyPostReadinessSchemaRequirementsIsIdempotent(t *testing.T) {
	tools := []llama.Tool{{
		Type: "function",
		Function: llama.ToolFunction{
			Name:       "search_content",
			Parameters: json.RawMessage(`{"type":"object","properties":{"pattern":{"type":"string"}},"required":["pattern"]}`),
		},
	}}
	first := applyPostReadinessSchemaRequirements(tools)
	second := applyPostReadinessSchemaRequirements(first)
	if string(first[0].Function.Parameters) != string(second[0].Function.Parameters) {
		t.Fatalf("expected applyPostReadinessSchemaRequirements to be idempotent\nfirst=%s\nsecond=%s", first[0].Function.Parameters, second[0].Function.Parameters)
	}
}

func TestStripFallbackJSONArtifactsDropsMetadataBlocks(t *testing.T) {
	input := `- Read internal/agent/runner.go lines 100-140
- Ran function_tracing discovery query with 11 layer(s); artifact: .idiom/discovery/queries/abc.json
{
  "id": "abc",
  "purpose": "function_tracing",
  "artifact_path": ".idiom/discovery/queries/abc.json",
  "layers_run": [
    "project_behavior_digest:artifact_read"
  ]
}
- Found relevant call site at runner.go:574`
	out := stripFallbackJSONArtifacts(input)
	if strings.Contains(out, `"id":`) || strings.Contains(out, `"layers_run":`) {
		t.Fatalf("expected JSON metadata block to be stripped, got:\n%s", out)
	}
	for _, want := range []string{
		"- Read internal/agent/runner.go lines 100-140",
		"- Ran function_tracing discovery query",
		"- Found relevant call site at runner.go:574",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected prose bullet %q to survive, got:\n%s", want, out)
		}
	}
}

func TestStripFallbackJSONArtifactsHandlesTruncatedDump(t *testing.T) {
	// In the live log the JSON block was cut mid-stream by upstream
	// truncation (no closing brace). Ensure the stripper still drops it.
	input := `- Ran discovery query
{
  "id": "abc",
  "layers_run": [
    "x",
...
- Final bullet`
	out := stripFallbackJSONArtifacts(input)
	if strings.Contains(out, `"id":`) {
		t.Fatalf("expected truncated JSON dump to be stripped, got:\n%s", out)
	}
	if !strings.Contains(out, "- Final bullet") {
		t.Fatalf("expected next bullet after truncated JSON to survive, got:\n%s", out)
	}
}

func TestStripFallbackJSONArtifactsLeavesProseUnchanged(t *testing.T) {
	input := `Verified context:
- Child task completed
- Inspected internal/agent/runner.go lines 1-40

Best-effort observations:
- Function runChildTask is the entry point.`
	out := stripFallbackJSONArtifacts(input)
	if out != input {
		t.Fatalf("expected prose-only input to be unchanged\ninput=%q\nout=%q", input, out)
	}
}

func TestPostReadinessContinuationContractDisclosesBudget(t *testing.T) {
	contract := postReadinessContinuationContract("delegated_read_only_answer_ready", 2)
	if !strings.Contains(contract, "at most 2 targeted probes") {
		t.Fatalf("expected contract to disclose budget=2, got:\n%s", contract)
	}
	if got := postReadinessContinuationContract("delegated_read_only_answer_ready", 1); !strings.Contains(got, "one targeted probe") {
		t.Fatalf("expected contract to disclose budget=1, got:\n%s", got)
	}
	if got := postReadinessContinuationContract("child_packet_available", 0); !strings.Contains(got, "no further reads before answering") {
		t.Fatalf("expected contract to state zero-budget, got:\n%s", got)
	}
	pkt := postReadinessContinuationContract("child_packet_available", 2)
	if !strings.Contains(pkt, "packet-named path or symbol") {
		t.Fatalf("expected child_packet contract to mention packet-named path, got:\n%s", pkt)
	}
}

func TestStripNestedFallbackTemplateRemovesOuterScaffolding(t *testing.T) {
	inner := "Partial findings from this turn (the budget was reached before a full synthesis):\n\n" +
		"Verified context:\n- Read internal/agent/runner.go lines 100-140\n- Found something\n\n" +
		"Final blocker:\n- Post-readiness follow-up missing named gap: the child packet already contains sufficient evidence...\n\n" +
		"Recommended next step:\n- if you continue later, name one exact missing claim"
	stripped := stripNestedFallbackTemplate(inner)
	if strings.Contains(stripped, "Partial findings from this turn") {
		t.Fatalf("expected outer header to be stripped, got:\n%s", stripped)
	}
	if strings.Contains(stripped, "Recommended next step:") {
		t.Fatalf("expected trailing 'Recommended next step:' to be stripped, got:\n%s", stripped)
	}
	if strings.Contains(stripped, "Final blocker:") {
		t.Fatalf("expected harness-rejection 'Final blocker:' section to be stripped, got:\n%s", stripped)
	}
	if !strings.Contains(stripped, "internal/agent/runner.go lines 100-140") {
		t.Fatalf("expected substantive evidence to survive stripping, got:\n%s", stripped)
	}
	// Idempotent on a clean string.
	clean := "- Read foo.go\n- Found bar"
	if got := stripNestedFallbackTemplate(clean); got != clean {
		t.Fatalf("expected idempotent on clean input, got %q", got)
	}
}

func TestIsHarnessRejectionSummaryRecognizesContractRejections(t *testing.T) {
	for _, summary := range []string{
		"Post-readiness follow-up missing named gap",
		"Post-child follow-up missing named gap",
		"Post-readiness follow-up is not an exact probe",
		"Post-child follow-up gap is not packet-anchored",
		"Broad inspection request rejected",
		"Chained list_files rejected",
	} {
		if !isHarnessRejectionSummary(summary) {
			t.Errorf("expected %q to be classified as a harness rejection", summary)
		}
	}
	for _, summary := range []string{
		"Failed to stat path",
		"File not found",
		"Edit request rejected",
	} {
		if isHarnessRejectionSummary(summary) {
			t.Errorf("did not expect %q to be classified as a harness rejection (real tool failure)", summary)
		}
	}
}

func TestInvalidPostReadinessGapContentRejectsParaphrase(t *testing.T) {
	objective := "Explain how this harness uses subagents."
	gap := "Explain how the harness uses subagents and how delegation works"
	summary, errorText, rejected := invalidPostReadinessGapContent("delegated_read_only_answer_ready", objective, workspace.ToolRequest{
		Tool:     "read_file",
		Path:     "internal/agent/runner_child_tasks.go",
		NamedGap: gap,
	})
	if !rejected {
		t.Fatalf("expected paraphrase gap to be rejected, got summary=%q error=%q", summary, errorText)
	}
	if !strings.Contains(strings.ToLower(summary), "restates the original question") {
		t.Fatalf("expected rejection summary to mention restated question, got %q", summary)
	}
}

func TestInvalidPostReadinessGapContentRejectsExplainerWithoutCodeToken(t *testing.T) {
	objective := "What is the failure mode here?"
	gap := "I want to understand how the system handles errors"
	summary, _, rejected := invalidPostReadinessGapContent("child_packet_available", objective, workspace.ToolRequest{
		Tool:     "read_file",
		Path:     "internal/agent/runner.go",
		NamedGap: gap,
	})
	if !rejected {
		t.Fatalf("expected explainer-without-code-token gap to be rejected, got summary=%q", summary)
	}
}

func TestInvalidPostReadinessGapContentAcceptsConcreteGap(t *testing.T) {
	objective := "Explain how this harness uses subagents."
	gap := "the body of `Runner.runChildTask` lines 4-36 — confirm budget plumbing"
	if _, _, rejected := invalidPostReadinessGapContent("child_packet_available", objective, workspace.ToolRequest{
		Tool:     "go_read_symbol",
		Path:     "internal/agent/runner_child_tasks.go",
		Pattern:  "runChildTask",
		NamedGap: gap,
	}); rejected {
		t.Fatal("did not expect a concrete gap (named symbol + line range) to be rejected")
	}
}

func TestRewriteVisibilityNoteExplainsRedirect(t *testing.T) {
	note := rewriteVisibilityNote("search_content", "task")
	if !strings.Contains(note, "`search_content`") || !strings.Contains(note, "`task`") {
		t.Fatalf("expected note to name both tools, got %q", note)
	}
	if !strings.Contains(note, "rewritten call will execute") {
		t.Fatalf("expected note to confirm execution, got %q", note)
	}
}

func TestNormalizeRunnerToolRequestIgnoresPostReadinessReasoningFields(t *testing.T) {
	a := normalizedToolRequestRaw(workspace.ToolRequest{
		Tool:            "search_content",
		Pattern:         "runner",
		Path:            "internal",
		NamedGap:        "find the exact stop-condition branch",
		WhyNotAnswering: "I still need the deciding condition",
	})
	b := normalizedToolRequestRaw(workspace.ToolRequest{
		Tool:            "search_content",
		Pattern:         "runner",
		Path:            "internal",
		NamedGap:        "a different phrasing",
		WhyNotAnswering: "different rationale text",
	})
	if a != b {
		t.Fatalf("expected normalized request fingerprint to ignore rationale fields, got %q vs %q", a, b)
	}
}

func TestRunnerReplaysEquivalentReadRequestWithNormalizedPath(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}

	replies := []string{
		`{"tool":"read_file","path":"./README.md","start_line":1,"end_line":1}`,
		`{"tool":"read_file","path":"README.md","start_line":1,"end_line":1}`,
		"Reused prior evidence instead of rereading the same file.",
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request #%d", requests)
		}
		reply := replies[requests]
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))
	defer server.Close()

	var replayed bool
	runner := &Runner{
		Client: llama.NewClient(server.URL, 2*time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 4, MaxNoProgressTurns: 4},
	}
	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Read README twice if needed."}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			if event.Request.Tool == "read_file" && strings.Contains(strings.ToLower(event.Result.Summary), "replayed prior evidence") {
				replayed = true
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if answer != "Reused prior evidence instead of rereading the same file." {
		t.Fatalf("unexpected answer: %q", answer)
	}
	if !replayed {
		t.Fatal("expected equivalent path variant to replay prior evidence")
	}
}

// Soft-failure nudge — runner must fire rejection_recovery_nudge when three
// tool calls in a row return errors against the same path stem, even though
// none of them are "hard rejections." Mirrors the failure mode we saw in
// logs/conversations/20260420-210101.488.jsonl where the model called
// read_file on five fabricated files inside a non-existent directory.
func TestRunnerEmitsRecoveryNudgeOnRepeatedSameStemSoftFailures(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "internal/agent/runner.go", "package agent\n"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	replies := []string{
		`{"tool":"read_file","path":"internal/llm/llm.go"}`,
		`{"tool":"read_file","path":"internal/llm/openai.go"}`,
		`{"tool":"read_file","path":"internal/llm/qwen.go"}`,
		"Stopped after recovery nudge.",
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request")
		}
		reply := replies[requests]
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": reply}}},
		})
	}))
	defer server.Close()

	mgr := workspace.NewManager(config.Workspace{
		Root:                root,
		CWD:                 root,
		MaxDirectoryEntries: 128,
	})
	var nudgeEmitted bool
	runner := &Runner{
		Client:    llama.NewClient(server.URL, 2*time.Second),
		Workspace: mgr,
		Policy:    RunPolicy{MaxToolSteps: 6, MaxNoProgressTurns: 6},
		OnEvent: func(event RunEvent) {
			if event.Role == "rejection_recovery_nudge" {
				nudgeEmitted = true
			}
		},
	}
	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "find the llm package"}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !nudgeEmitted {
		t.Fatal("expected rejection_recovery_nudge after 3 same-stem soft failures")
	}
	if answer != "Stopped after recovery nudge." {
		t.Fatalf("unexpected answer: %q", answer)
	}
}

// TestDoomLoopGateDenyPathStops verifies that when a DoomLoopGate is wired and
// returns false, the runner stops with stop_reason=doom_loop_guard after
// forcing a no-tools closeout from gathered evidence. Proves the gate is
// reachable and its deny path does not return an empty answer.
func TestDoomLoopGateDenyPathStops(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "internal/app/app.go", "package app\n"); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	replies := []string{
		`{"tool":"list_files","path":".","recursive":true}`,
		`{"tool":"list_files","path":".","recursive":true}`,
		`{"tool":"list_files","path":".","recursive":true}`,
		"Answered after doom-loop denial.",
	}
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request (%d)", requests)
		}
		reply := replies[requests]
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))
	defer server.Close()

	mgr := workspace.NewManager(config.Workspace{Root: root, CWD: root})
	if err := mgr.SetMode(workspace.ModeExplore); err != nil {
		t.Fatalf("set explore mode: %v", err)
	}

	gateCalls := 0
	runner := &Runner{
		Client:    llama.NewClient(server.URL, time.Second),
		Workspace: mgr,
		Policy:    RunPolicy{MaxNoProgressTurns: 6},
		DoomLoopGate: func(req workspace.ToolRequest, count int) bool {
			gateCalls++
			if count < 3 {
				t.Fatalf("gate invoked with count=%d, expected >=3", count)
			}
			return false
		},
	}

	var stopReason string
	runner.OnEvent = func(event RunEvent) {
		if event.Role == "budget_ledger" {
			var ledger runnerBudgetLedger
			if err := json.Unmarshal([]byte(event.Content), &ledger); err == nil {
				stopReason = ledger.StopReason
			}
		}
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "loop on broad listing"}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if gateCalls != 1 {
		t.Fatalf("expected DoomLoopGate to be called exactly once, got %d", gateCalls)
	}
	if stopReason != "doom_loop_guard" {
		t.Fatalf("expected stop_reason=doom_loop_guard, got %q", stopReason)
	}
	if answer != "Answered after doom-loop denial." {
		t.Fatalf("expected forced closeout answer, got %q", answer)
	}
	if requests != 4 {
		t.Fatalf("expected three loop requests plus one forced closeout, got %d", requests)
	}
}

// TestDoomLoopGateApprovePathContinues verifies the approve branch: gate
// returns true, the streak is reset, and execution continues past the
// 3-identical-calls threshold. After approval the model emits a non-tool
// answer and the turn completes normally (no doom_loop_guard stop).
func TestDoomLoopGateApprovePathContinues(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "internal/app/app.go", "package app\n"); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	replies := []string{
		`{"tool":"list_files","path":".","recursive":true}`,
		`{"tool":"list_files","path":".","recursive":true}`,
		`{"tool":"list_files","path":".","recursive":true}`,
		"Proceeded past approved loop; answering now.",
	}
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request (%d)", requests)
		}
		reply := replies[requests]
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))
	defer server.Close()

	mgr := workspace.NewManager(config.Workspace{Root: root, CWD: root})
	if err := mgr.SetMode(workspace.ModeExplore); err != nil {
		t.Fatalf("set explore mode: %v", err)
	}

	gateCalls := 0
	runner := &Runner{
		Client:    llama.NewClient(server.URL, time.Second),
		Workspace: mgr,
		Policy:    RunPolicy{MaxNoProgressTurns: 6},
		DoomLoopGate: func(req workspace.ToolRequest, count int) bool {
			gateCalls++
			return true
		},
	}

	var stopReason string
	runner.OnEvent = func(event RunEvent) {
		if event.Role == "budget_ledger" {
			var ledger runnerBudgetLedger
			if err := json.Unmarshal([]byte(event.Content), &ledger); err == nil {
				stopReason = ledger.StopReason
			}
		}
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "loop on broad listing"}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if gateCalls < 1 {
		t.Fatalf("expected DoomLoopGate to be called at least once, got %d", gateCalls)
	}
	if stopReason == "doom_loop_guard" {
		t.Fatalf("approve path should NOT stop with doom_loop_guard, got stop_reason=%q", stopReason)
	}
	if answer != "Proceeded past approved loop; answering now." {
		t.Fatalf("unexpected answer after approve path: %q", answer)
	}
	if requests != 4 {
		t.Fatalf("expected 4 model requests (3 tool loops + 1 final answer), got %d", requests)
	}
}

// runNudgeScenario drives a canned model transcript against Runner.Run and
// returns the set of emitted event roles. Used by the nudge tests below to
// assert that soft-budget nudges fire the expected number of times without
// recreating the httptest scaffold each time.
func runNudgeScenario(t *testing.T, policy RunPolicy, replies []string) (events []RunEvent, requests int) {
	t.Helper()
	root := t.TempDir()
	if err := writeTestFile(root, "internal/app/app.go", "package app\n"); err != nil {
		t.Fatalf("write seed: %v", err)
	}
	if err := writeTestFile(root, "internal/agent/runner.go", "package agent\n"); err != nil {
		t.Fatalf("write seed: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request (%d)", requests)
		}
		reply := replies[requests]
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": reply}}},
		})
	}))
	t.Cleanup(server.Close)

	mgr := workspace.NewManager(config.Workspace{Root: root, CWD: root})
	if err := mgr.SetMode(workspace.ModeExplore); err != nil {
		t.Fatalf("set explore mode: %v", err)
	}

	runner := &Runner{
		Client:    llama.NewClient(server.URL, time.Second),
		Workspace: mgr,
		Policy:    policy,
		OnEvent: func(event RunEvent) {
			events = append(events, event)
		},
	}
	_, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "nudge scenario"}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return events, requests
}

func countEventsByRole(events []RunEvent, role string) int {
	n := 0
	for _, ev := range events {
		if ev.Role == role {
			n++
		}
	}
	return n
}

// TestToolFailureStreakTerminatesAfterRecoveryNudge verifies the bail
// path: if a path-stem tool-failure streak continues after the soft
// recovery nudge has already fired once, the runner terminates the
// turn instead of letting the model churn indefinitely. Observed in a
// live doc-edit task where the model burned 60+ tool calls retrying
// edit_file with progressively broken arg shapes.
func TestToolFailureStreakTerminatesAfterRecoveryNudge(t *testing.T) {
	// All reads target paths inside the same top-level dir (same path
	// stem) but each path is non-existent, so workspace.Execute fails
	// per call — not a guard rejection, a tool-execution error.
	// Three failures fires the soft recovery nudge. Three more should
	// trip the new hard bail. start_line/end_line are set so the
	// redirect-to-outline doesn't intercept.
	replies := []string{
		`{"tool":"read_file","path":"internal/app/missing_a.go","start_line":1,"end_line":2}`,
		`{"tool":"read_file","path":"internal/app/missing_b.go","start_line":1,"end_line":2}`,
		`{"tool":"read_file","path":"internal/app/missing_c.go","start_line":1,"end_line":2}`,
		// 3rd failure on same stem → softFailureNudge fires, counter resets.
		`{"tool":"read_file","path":"internal/app/missing_d.go","start_line":1,"end_line":2}`,
		`{"tool":"read_file","path":"internal/app/missing_e.go","start_line":1,"end_line":2}`,
		`{"tool":"read_file","path":"internal/app/missing_f.go","start_line":1,"end_line":2}`,
		// 3rd failure of the 2nd streak → runner should bail with
		// tool_failure_streak_exhausted. The model never gets to emit
		// "final answer".
		"final answer",
	}
	events, _ := runNudgeScenario(t, RunPolicy{MaxNoProgressTurns: 20}, replies)

	seenRecovery := false
	seenBail := false
	for _, ev := range events {
		if ev.Role == "rejection_recovery_nudge" {
			seenRecovery = true
		}
		if strings.Contains(ev.Content, "tool_failure_streak_exhausted") {
			seenBail = true
		}
	}
	if !seenRecovery {
		t.Fatalf("expected recovery nudge to fire on first 3-failure streak, events: %v", eventRolesFor(events))
	}
	// Fallback for bail: check the budget_ledger stop_reason.
	for _, ev := range events {
		if ev.Role == "budget_ledger" && strings.Contains(ev.Content, "tool_failure_streak_exhausted") {
			seenBail = true
			break
		}
	}
	if !seenBail {
		t.Fatalf("expected tool_failure_streak_exhausted stop reason after second streak, events: %v", eventRolesFor(events))
	}
}

// TestZeroHitRetryBlocked verifies that a second search_files call with
// a pattern that returned no matches on the first call is rejected with
// the "Zero-hit retry blocked" guard — the model doesn't get to burn
// another budget slot on a call the search space guarantees will again
// return nothing.
func TestZeroHitRetryBlocked(t *testing.T) {
	replies := []string{
		`{"tool":"search_files","pattern":"nonexistent_xyz_token"}`,
		`{"tool":"search_files","pattern":"nonexistent_xyz_token"}`,
		"final answer",
	}
	events, _ := runNudgeScenario(t, RunPolicy{MaxNoProgressTurns: 8}, replies)

	found := false
	for _, ev := range events {
		if strings.Contains(ev.Content, "Zero-hit retry blocked") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected zero-hit retry guard to fire, events: %v", eventRolesFor(events))
	}
}

func TestSuccessfulSearchMustBeConsumedBeforeAnotherSearch(t *testing.T) {
	replies := []string{
		`{"tool":"search_files","path":"internal","pattern":"*runner*"}`,
		`{"tool":"search_files","path":"internal","pattern":"*task*"}`,
		"final answer",
	}
	events, _ := runNudgeScenario(t, RunPolicy{MaxNoProgressTurns: 8}, replies)

	found := false
	for _, ev := range events {
		if strings.Contains(ev.Content, "Chained search rejected") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected chained-search guard to fire, events: %v", eventRolesFor(events))
	}
}

func TestSuccessfulSearchCanContinueAfterConcreteInspection(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "internal/app/app.go", "package app\n"); err != nil {
		t.Fatalf("write seed: %v", err)
	}
	if err := writeTestFile(root, "internal/agent/runner.go", "package agent\n"); err != nil {
		t.Fatalf("write seed: %v", err)
	}
	replies := []string{
		`{"tool":"search_files","path":"internal","pattern":"*runner*"}`,
		`{"tool":"search_files","path":"internal","pattern":"*task*"}`,
		`{"tool":"read_file","path":"internal/agent/runner.go","start_line":1,"end_line":1}`,
		`{"tool":"search_files","path":"internal","pattern":"*app*"}`,
		"final answer",
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request (%d)", requests)
		}
		reply := replies[requests]
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": reply}}},
		})
	}))
	t.Cleanup(server.Close)

	mgr := workspace.NewManager(config.Workspace{Root: root, CWD: root})
	if err := mgr.SetMode(workspace.ModeExplore); err != nil {
		t.Fatalf("set explore mode: %v", err)
	}

	var events []RunEvent
	var results []workspace.ToolResult
	runner := &Runner{
		Client:    llama.NewClient(server.URL, time.Second),
		Workspace: mgr,
		Policy:    RunPolicy{MaxNoProgressTurns: 8},
		OnEvent: func(event RunEvent) {
			events = append(events, event)
		},
	}
	_, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "nudge scenario"}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			results = append(results, event.Result)
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	successfulSearches := 0
	for _, result := range results {
		if result.Tool == "search_files" && result.Error == "" {
			successfulSearches++
		}
		if result.Summary == "Tool hidden by active contract" && strings.Contains(result.Error, "search_files") {
			t.Fatalf("search_files should not stay hidden after concrete inspection; results=%#v events=%v", results, eventRolesFor(events))
		}
	}
	if successfulSearches != 2 {
		t.Fatalf("expected first and post-inspection searches to execute, got %d results=%#v", successfulSearches, results)
	}
}

// TestRejectionRetryBlocked verifies that when a pre-execute guard
// rejects a tool call, a byte-identical retry on a later step
// short-circuits the turn via rejection_retry_blocked instead of
// replaying the rejection cycle.
func TestRejectionRetryBlocked(t *testing.T) {
	// A repeated root list_files in explore mode trips the broad-inspection
	// guard without using the chained-list_files hiding path. A later retry of
	// that same rejected fingerprint should trigger rejection_retry_blocked.
	rejectedArgs := `{"tool":"list_files","path":"."}`
	replies := []string{
		`{"tool":"list_files","path":"."}`,
		`{"tool":"list_files","path":"internal/agent"}`,
		rejectedArgs, // rejected by broad-inspection guard
		`{"tool":"read_file","path":"internal/app/app.go"}`,
		rejectedArgs, // retry of the rejected fingerprint — blocked
		"final answer",
	}
	events, _ := runNudgeScenario(t, RunPolicy{MaxNoProgressTurns: 8}, replies)

	if got := countEventsByRole(events, "rejection_retry_blocked"); got != 1 {
		t.Fatalf("expected exactly 1 rejection_retry_blocked event, got %d; events: %v", got, eventRolesFor(events))
	}
}

func TestCorrectedNamedGapRetryIsAllowedOnce(t *testing.T) {
	replies := []string{
		`{"tool":"discovery_query","purpose":"architecture_boundary","path":"internal/agent","pattern":"task|delegate"}`,
		`{"tool":"read_file","path":"internal/agent/runner.go"}`,
		`{"tool":"read_file","path":"internal/agent/runner.go","named_gap":"inspect the exact branch that launches the child task"}`,
		"answer from the repaired follow-up",
	}

	root := t.TempDir()
	if err := writeTestFile(root, "internal/agent/runner.go", "package agent\nfunc runChildTask() {}\n"); err != nil {
		t.Fatalf("write seed: %v", err)
	}
	serverRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serverRequests >= len(replies) {
			t.Fatalf("unexpected extra chat request (%d)", serverRequests)
		}
		reply := replies[serverRequests]
		serverRequests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": reply}}},
		})
	}))
	defer server.Close()

	mgr := workspace.NewManager(config.Workspace{Root: root, CWD: root})
	runner := &Runner{
		Client:    llama.NewClient(server.URL, time.Second),
		Workspace: mgr,
		Policy:    RunPolicy{MaxToolSteps: 6, MaxNoProgressTurns: 8},
		AgentName: "child:explorer",
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "delegated_read_only",
			PermissionProfile: "read_only_workspace",
			Objective:         "Explain sub-agent usage.",
		},
	}

	var events []RunEvent
	runner.OnEvent = func(event RunEvent) {
		events = append(events, event)
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Explain sub-agent usage."}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if answer != "answer from the repaired follow-up" {
		t.Fatalf("unexpected answer %q", answer)
	}
	foundMissingGapRejection := false
	foundRepeatedDeadEnd := false
	foundToolReplayed := false
	for _, ev := range events {
		if strings.Contains(ev.Content, "Post-readiness follow-up missing named gap") {
			foundMissingGapRejection = true
		}
		if strings.Contains(ev.Content, "Repeated dead-end action rejected") {
			foundRepeatedDeadEnd = true
		}
		if ev.Role == "tool_replayed" || strings.Contains(ev.Content, "replayed same blocker") {
			foundToolReplayed = true
		}
	}
	if !foundMissingGapRejection {
		t.Fatalf("expected repaired-retry scenario to first hit the missing named-gap rejection, roles=%v", eventRolesFor(events))
	}
	if foundRepeatedDeadEnd {
		t.Fatalf("corrected named-gap retry should not be reclassified as a repeated dead end, roles=%v", eventRolesFor(events))
	}
	if foundToolReplayed {
		t.Fatalf("corrected named-gap retry should execute, not replay the prior missing-gap blocker, roles=%v", eventRolesFor(events))
	}
	if got := countEventsByRole(events, "rejection_retry_blocked"); got != 0 {
		t.Fatalf("expected no rejection_retry_blocked after corrected named-gap retry, got %d; roles=%v", got, eventRolesFor(events))
	}
	if got := countEventsByRole(events, "post_readiness_gap_declared"); got != 1 {
		t.Fatalf("expected corrected retry to declare a named gap once, got %d; roles=%v", got, eventRolesFor(events))
	}
}

func eventRolesFor(events []RunEvent) []string {
	out := make([]string, 0, len(events))
	seen := make(map[string]bool)
	for _, ev := range events {
		if !seen[ev.Role] {
			seen[ev.Role] = true
			out = append(out, ev.Role)
		}
	}
	return out
}

// TestFingerprintNudgeFiresOncePerFingerprint verifies that when the model
// calls the same tool+args three times *non-consecutively*, the runner
// emits a fingerprint_nudge exactly once — and does NOT trigger the
// DoomLoopGate (which requires consecutive triples).
func TestFingerprintNudgeFiresOncePerFingerprint(t *testing.T) {
	// Alternate A/B/A/B/A so the identical-call run never reaches 3.
	replies := []string{
		`{"tool":"read_file","path":"internal/app/app.go"}`,
		`{"tool":"read_file","path":"internal/agent/runner.go"}`,
		`{"tool":"read_file","path":"internal/app/app.go"}`,
		`{"tool":"read_file","path":"internal/agent/runner.go"}`,
		`{"tool":"read_file","path":"internal/app/app.go"}`,
		"done",
	}
	policy := RunPolicy{MaxNoProgressTurns: 8, FingerprintNudgeAt: 3}
	events, _ := runNudgeScenario(t, policy, replies)

	if got := countEventsByRole(events, "fingerprint_nudge"); got != 1 {
		t.Fatalf("expected exactly 1 fingerprint_nudge, got %d", got)
	}
	if got := countEventsByRole(events, "doom_loop_guard"); got != 0 {
		t.Fatalf("DoomLoopGate should NOT fire on non-consecutive repeats, got %d", got)
	}
}

// TestSynthesisNudgeFiresOnceAtThreshold verifies that once the tool-call
// count crosses SynthesisNudgeAt, the runner emits exactly one
// synthesis_nudge for the whole turn.
func TestSynthesisNudgeFiresOnceAtThreshold(t *testing.T) {
	replies := []string{
		`{"tool":"list_files","path":"internal/app","recursive":false}`,
		`{"tool":"list_files","path":"internal/agent","recursive":false}`,
		`{"tool":"list_files","path":".","recursive":false}`,
		`{"tool":"list_files","path":"internal","recursive":false}`,
		"final answer",
	}
	policy := RunPolicy{MaxNoProgressTurns: 8, SynthesisNudgeAt: 3}
	events, _ := runNudgeScenario(t, policy, replies)

	if got := countEventsByRole(events, "synthesis_nudge"); got != 1 {
		t.Fatalf("expected exactly 1 synthesis_nudge, got %d", got)
	}
}

// TestNudgesDoNotFireWhenDisabled verifies that zero-valued thresholds
// keep the nudges dormant — the feature is opt-in per mode.
func TestNudgesDoNotFireWhenDisabled(t *testing.T) {
	replies := []string{
		`{"tool":"list_files","path":"internal/app","recursive":false}`,
		`{"tool":"list_files","path":"internal/agent","recursive":false}`,
		`{"tool":"list_files","path":".","recursive":false}`,
		`{"tool":"list_files","path":"internal","recursive":false}`,
		"done",
	}
	policy := RunPolicy{MaxNoProgressTurns: 8}
	events, _ := runNudgeScenario(t, policy, replies)

	for _, role := range []string{"fingerprint_nudge", "synthesis_nudge"} {
		if got := countEventsByRole(events, role); got != 0 {
			t.Fatalf("expected no %s events when disabled, got %d", role, got)
		}
	}
}

// TestModePolicyNudgeDefaults documents the ModePolicy defaults so future
// edits to the numbers are an explicit, reviewed change.
func TestModePolicyNudgeDefaults(t *testing.T) {
	plan := ModePolicy(PlanMode())
	if plan.FingerprintNudgeAt != 3 {
		t.Fatalf("plan FingerprintNudgeAt: want 3, got %d", plan.FingerprintNudgeAt)
	}
	if plan.SynthesisNudgeAt != 10 {
		t.Fatalf("plan SynthesisNudgeAt: want 10, got %d", plan.SynthesisNudgeAt)
	}
	build := ModePolicy(BuildMode())
	if build.FingerprintNudgeAt != 3 || build.SynthesisNudgeAt != 10 {
		t.Fatalf("build nudge thresholds: want (3, 10), got (%d, %d)", build.FingerprintNudgeAt, build.SynthesisNudgeAt)
	}
}

// runRedirectScenario stands up a tiny Go workspace (go.mod + one .go file)
// so the language detector activates, then drives a canned transcript.
func runRedirectScenario(t *testing.T, replies []string) (events []RunEvent, requests int) {
	t.Helper()
	root := t.TempDir()
	if err := writeTestFile(root, "go.mod", "module example.test\n"); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	// Pad past the small-file redirect threshold so these redirect
	// scenarios actually exercise the redirect path. Files below the
	// threshold are left to execute directly because the outline detour
	// isn't worth its cost on tiny files.
	if err := writeTestFile(root, "main.go", "package main\n\nfunc main() {}\n"+strings.Repeat("\n", 500)); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request (%d)", requests)
		}
		reply := replies[requests]
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": reply}}},
		})
	}))
	t.Cleanup(server.Close)

	mgr := workspace.NewManager(config.Workspace{Root: root, CWD: root})
	runner := &Runner{
		Client:    llama.NewClient(server.URL, time.Second),
		Workspace: mgr,
		Policy:    RunPolicy{MaxNoProgressTurns: 8},
		OnEvent: func(event RunEvent) {
			events = append(events, event)
		},
	}
	_, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "redirect scenario"}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return events, requests
}

// TestDiscoveryRedirectOnReadFileCodePath asserts that a read_file on a
// Go source file (no line range) is intercepted and redirected to
// go_file_outline, with the decision guide appended once.
func TestDiscoveryRedirectOnReadFileCodePath(t *testing.T) {
	replies := []string{
		`{"tool":"read_file","path":"main.go"}`,
		"final answer",
	}
	events, _ := runRedirectScenario(t, replies)
	if got := countEventsByRole(events, "discovery_redirect"); got != 1 {
		t.Fatalf("expected exactly 1 discovery_redirect event, got %d", got)
	}
	if got := countEventsByRole(events, "discovery_decision_guide"); got != 1 {
		t.Fatalf("expected exactly 1 discovery_decision_guide event, got %d", got)
	}
}

// TestDiscoveryRedirectFallsBackOnSecondCall asserts the one-shot
// semantics: the second identical generic call after a redirect is
// allowed through so the model can still fall back.
func TestDiscoveryRedirectFallsBackOnSecondCall(t *testing.T) {
	replies := []string{
		`{"tool":"read_file","path":"main.go"}`,
		`{"tool":"read_file","path":"main.go"}`,
		"final",
	}
	events, _ := runRedirectScenario(t, replies)
	if got := countEventsByRole(events, "discovery_redirect"); got != 1 {
		t.Fatalf("expected exactly 1 discovery_redirect, got %d", got)
	}
}

// TestDiscoveryRedirectSkipsRangedReads ensures a read_file with an
// explicit start_line/end_line is treated as targeted and NOT redirected.
func TestDiscoveryRedirectSkipsRangedReads(t *testing.T) {
	replies := []string{
		`{"tool":"read_file","path":"main.go","start_line":1,"end_line":3}`,
		"final",
	}
	events, _ := runRedirectScenario(t, replies)
	if got := countEventsByRole(events, "discovery_redirect"); got != 0 {
		t.Fatalf("ranged reads should not redirect, got %d", got)
	}
}

// TestDiscoveryRedirectSkipsStructuredTool ensures a language-specific
// tool is never redirected to itself or another structured tool.
func TestDiscoveryRedirectSkipsStructuredTool(t *testing.T) {
	replies := []string{
		`{"tool":"go_overview"}`,
		"final",
	}
	events, _ := runRedirectScenario(t, replies)
	if got := countEventsByRole(events, "discovery_redirect"); got != 0 {
		t.Fatalf("structured tools should not redirect, got %d", got)
	}
}

// TestDiscoveryRedirectDecisionGuideFiresOncePerLanguage confirms the
// full decision guide is injected on the first redirect per language
// and omitted on subsequent redirects in the same turn.
func TestDiscoveryRedirectDecisionGuideFiresOncePerLanguage(t *testing.T) {
	// Two distinct read_file targets — each fires a redirect, but only
	// the first attaches the decision guide.
	replies := []string{
		`{"tool":"read_file","path":"main.go"}`,
		`{"tool":"read_file","path":"other.go"}`,
		"final",
	}
	// Ensure the second target exists.
	events, _ := func() ([]RunEvent, int) {
		root := t.TempDir()
		if err := writeTestFile(root, "go.mod", "module example.test\n"); err != nil {
			t.Fatalf("go.mod: %v", err)
		}
		// Pad both files past the small-file redirect threshold so the
		// outline redirect fires for each.
		if err := writeTestFile(root, "main.go", "package main\nfunc main(){}\n"+strings.Repeat("\n", 500)); err != nil {
			t.Fatalf("main.go: %v", err)
		}
		if err := writeTestFile(root, "other.go", "package main\nfunc other(){}\n"+strings.Repeat("\n", 500)); err != nil {
			t.Fatalf("other.go: %v", err)
		}
		requests := 0
		var events []RunEvent
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			reply := replies[requests]
			requests++
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{{"message": map[string]string{"content": reply}}},
			})
		}))
		t.Cleanup(server.Close)
		mgr := workspace.NewManager(config.Workspace{Root: root, CWD: root})
		runner := &Runner{
			Client:    llama.NewClient(server.URL, time.Second),
			Workspace: mgr,
			Policy:    RunPolicy{MaxNoProgressTurns: 8},
			OnEvent:   func(ev RunEvent) { events = append(events, ev) },
		}
		_, err := runner.Run(
			context.Background(),
			[]llama.ChatMessage{{Role: "user", Content: "multi redirect"}},
			llama.ChatOptions{},
			nil, nil, nil,
		)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		return events, requests
	}()
	if got := countEventsByRole(events, "discovery_redirect"); got != 2 {
		t.Fatalf("expected 2 discovery_redirect events, got %d", got)
	}
	if got := countEventsByRole(events, "discovery_decision_guide"); got != 1 {
		t.Fatalf("expected 1 discovery_decision_guide (one-shot per language), got %d", got)
	}
}

func TestRunnerClosureGuidanceActiveSuppressesDecisionGuideAfterPostChildClosure(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:    "architecture",
		Objective:     "Explain how this harness uses subagents.",
		Constraints:   []string{"child_packet_available"},
		EvidencePaths: []string{"internal/agent/runner.go", "internal/agent/runner_child_tasks.go"},
	}
	if !runnerClosureGuidanceActive("main", brief, []workspace.ToolResult{{
		Tool:    "go_file_outline",
		Summary: "Outlined 12 top-level declaration(s) in internal/agent/runner.go.",
	}}, false, false) {
		t.Fatal("expected durable child-packet closure to suppress discovery decision-guide reinjection")
	}
}
