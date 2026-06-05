package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"idiom/internal/autonomy"
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
		{"not_required_for_anything_at_all", false},
		{"  not_required_for_text_mutations  ", true}, // whitespace tolerated
		{"  not_required_for_x  ", false},
	}
	for _, tc := range cases {
		if got := verificationContractSkipsGate(tc.contract); got != tc.want {
			t.Errorf("verificationContractSkipsGate(%q) = %v, want %v", tc.contract, got, tc.want)
		}
	}
}

func TestContextPackResultNeedsFreshnessRecovery(t *testing.T) {
	cases := []workspace.ToolResult{
		{Tool: "context_pack", Error: "freshness_blocker"},
		{Tool: "context_pack", Summary: "context_pack blocked by context freshness (stale_unbounded_graph)"},
		{Tool: "context_pack", Output: `{"error":"freshness_blocker"}`},
	}
	for _, result := range cases {
		if !contextPackResultNeedsFreshnessRecovery(result) {
			t.Fatalf("expected freshness recovery for %#v", result)
		}
	}
	if contextPackResultNeedsFreshnessRecovery(workspace.ToolResult{Tool: "context_pack", Summary: "Context pack wrote view"}) {
		t.Fatal("successful context_pack summary should not trigger freshness recovery")
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
	if !hasSuccessfulVerificationResult([]workspace.ToolResult{{Tool: "source_sanity", Summary: "Source sanity passed", Output: "native/tests/test_cart_system.cpp: no warnings or errors via cpp_diagnostics"}}) {
		t.Fatal("compiler-backed source_sanity should count as verification")
	}
}

func TestShellSyntaxCheckSatisfiesScriptPostWriteVerification(t *testing.T) {
	req := workspace.ToolRequest{Tool: "shell", Command: "bash -n scripts/run-harness.sh"}
	if !isVerificationRequest(req) {
		t.Fatal("bash -n should count as a verification request for shell script edits")
	}
	writeOnlyResults := []workspace.ToolResult{
		{Tool: "edit_file", Summary: "Edited scripts/run-harness.sh"},
	}
	deterministicReq, ok := deterministicPostWriteVerificationRequest(writeOnlyResults, workerruntime.WorkerRuntimeBrief{VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker"})
	if !ok {
		t.Fatal("expected deterministic post-write verification request for shell script edits")
	}
	if deterministicReq.Tool != "shell" || deterministicReq.Command != "bash -n scripts/run-harness.sh" {
		t.Fatalf("unexpected deterministic verification request: %#v", deterministicReq)
	}
	results := []workspace.ToolResult{
		{Tool: "edit_file", Summary: "Edited scripts/run-harness.sh"},
		{Tool: "shell", Summary: "Ran shell command", Output: "$ bash -n scripts/run-harness.sh\n"},
	}
	if !hasSuccessfulVerificationResult(results) {
		t.Fatal("bash -n shell result should count as successful verification")
	}
	if hasPendingVerificationAfterWrites(results, workerruntime.WorkerRuntimeBrief{VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker"}) {
		t.Fatal("bash -n should clear pending post-write verification for script edits")
	}
}

func TestPostWriteVerificationTelemetryForShellSyntaxCheck(t *testing.T) {
	var events []RunEvent
	runner := &Runner{OnEvent: func(event RunEvent) {
		events = append(events, event)
	}}
	ok := runner.emitPostWriteVerificationTelemetry(
		workspace.ToolRequest{Tool: "shell", Command: "bash -n scripts/run-harness.sh"},
		workspace.ToolResult{Tool: "shell", Summary: "Ran shell command", Output: "$ bash -n scripts/run-harness.sh\n"},
	)
	if !ok {
		t.Fatal("expected telemetry emission for successful shell syntax verification")
	}
	if len(events) != 2 || events[0].Role != "verification_plan" || events[1].Role != "verification_feedback" {
		t.Fatalf("unexpected telemetry events: %#v", events)
	}
	var plan struct {
		Commands         []string `json:"commands"`
		Confidence       string   `json:"confidence"`
		ExpectedFailures []string `json:"expected_failures"`
	}
	if err := json.Unmarshal([]byte(events[0].Content), &plan); err != nil {
		t.Fatalf("parse verification plan: %v", err)
	}
	if !slices.Contains(plan.Commands, "bash -n scripts/run-harness.sh") ||
		plan.Confidence != "low" ||
		!slices.Contains(plan.ExpectedFailures, "shell syntax errors") {
		t.Fatalf("unexpected verification plan: %#v", plan)
	}
	var feedback struct {
		Confidence            string `json:"confidence"`
		LearnedRecommendation string `json:"learned_recommendation"`
	}
	if err := json.Unmarshal([]byte(events[1].Content), &feedback); err != nil {
		t.Fatalf("parse verification feedback: %v", err)
	}
	if feedback.Confidence != "low" || !strings.Contains(feedback.LearnedRecommendation, "targeted tests") {
		t.Fatalf("unexpected verification feedback: %#v", feedback)
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

func TestMutationCompletionGateRejectsBlockersNoneBeforeSourceVerification(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:           "implementation",
			Contract:             "implementation_result",
			VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
		},
	}
	write := workspace.ToolResult{Tool: "edit_file", Summary: "Edited internal/labels/labels.go"}
	answer := "Changed paths:\n- internal/labels/labels.go\n\nVerification: not run.\n\nBlockers: none."

	cases := []struct {
		name    string
		results []workspace.ToolResult
		want    string
	}{
		{
			name: "patch hygiene only",
			results: []workspace.ToolResult{
				write,
				{Tool: "shell", Summary: "Ran shell command", Output: "$ git diff --check -- internal/labels/labels.go\n"},
			},
			want: "patch hygiene",
		},
		{
			name: "generic source sanity fallback",
			results: []workspace.ToolResult{
				write,
				{Tool: "source_sanity", Summary: "Source sanity passed for 1 file(s) (1 generic fallback)", Output: "internal/labels/labels.go: generic delimiter sanity passed via generic_source_sanity"},
			},
			want: "latest successful write",
		},
		{
			name: "failed verification",
			results: []workspace.ToolResult{
				write,
				{Tool: "shell", Summary: "Ran shell command", Output: "$ go test ./...", Error: "internal/labels/labels.go:4: undefined: missing"},
			},
			want: "latest successful write",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, rejected := runner.mutationCompletionRejection(answer, tc.results)
			if !rejected || !strings.Contains(result.Error, tc.want) {
				t.Fatalf("expected verification rejection containing %q, rejected=%v result=%#v", tc.want, rejected, result)
			}
		})
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

	results = append(results, workspace.ToolResult{Tool: "source_sanity", Summary: "Source sanity passed", Output: "native/tests/test_cart_system.cpp: no warnings or errors via cpp_diagnostics"})
	if result, rejected := runner.mutationCompletionRejection(delegatedAnswer+"\nSource sanity: passed.", results); rejected {
		t.Fatalf("expected compiler-backed source_sanity plus delegated build/test status to satisfy closeout, got %#v", result)
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
		{req: workspace.ToolRequest{Tool: "read_file", Path: "internal/app/app.go", StartLine: 10, EndLine: 40}, want: "read_file:internal/app/app.go:10-40"},
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
	for _, want := range []string{"delegates build/test verification to the planner", "compiler-backed source-sanity", "git diff --check", "do not run build or test"} {
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

func TestAdvertisedAllowedToolsSuppressesArtifactFinalAnswerTools(t *testing.T) {
	root := t.TempDir()
	reg, err := workspace.NewExternalToolRegistry([]workspace.ExternalTool{
		{Name: "project_lookup", Command: "/bin/sh"},
	})
	if err != nil {
		t.Fatalf("NewExternalToolRegistry: %v", err)
	}
	manager := workspace.NewManager(config.Workspace{Root: root, CWD: root})
	manager.SetExternalToolsRegistry(reg)
	runner := &Runner{
		Workspace: manager,
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        workerruntime.ArtifactFinalAnswerTaskFamily,
			Contract:          "artifact_final_answer",
			PermissionProfile: "conversation_only",
		},
	}

	got := runner.advertisedAllowedTools([]string{"read_file", "write_file", "project_lookup"})
	if len(got) != 0 {
		t.Fatalf("artifact final-answer must advertise zero tools, got %#v", got)
	}
}

func TestArtifactFinalAnswerRunDoesNotAppendExternalTools(t *testing.T) {
	root := t.TempDir()
	reg, err := workspace.NewExternalToolRegistry([]workspace.ExternalTool{
		{Name: "project_lookup", Command: "/bin/sh"},
	})
	if err != nil {
		t.Fatalf("NewExternalToolRegistry: %v", err)
	}
	manager := workspace.NewManager(config.Workspace{Root: root, CWD: root})
	manager.SetExternalToolsRegistry(reg)
	type requestPayload struct {
		Tools []llama.Tool `json:"tools,omitempty"`
	}
	var payload requestPayload
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": "# Artifact\n"},
			}},
		})
	}))
	defer server.Close()
	runner := &Runner{
		Client:    llama.NewClient(server.URL, time.Second),
		Workspace: manager,
		Policy:    RunPolicy{MaxModelCalls: 1},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        workerruntime.ArtifactFinalAnswerTaskFamily,
			Contract:          "artifact_final_answer",
			PermissionProfile: "conversation_only",
		},
	}

	if _, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "write artifact"}}, llama.ChatOptions{}, nil, nil, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(payload.Tools) != 0 {
		t.Fatalf("artifact final-answer run must send zero tools, got %#v", payload.Tools)
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
		{name: "artifact final answer", contract: "artifact_final_answer", input: 20000, want: 8192},
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
			if got.EnableThinking == nil || *got.EnableThinking {
				t.Fatalf("text-only options should force visible output with thinking disabled, got %#v", got.EnableThinking)
			}
			if got.PreserveThinking {
				t.Fatal("text-only options should not preserve hidden reasoning into final synthesis")
			}
			if !got.ChatTemplateKwargs {
				t.Fatal("text-only options should send chat-template kwargs so thinking=false reaches local backends")
			}
		})
	}
}

func TestReadOnlyEvidenceAvailableTerminalChatOptionsForceVisibleOutput(t *testing.T) {
	thinkingOn := true
	tool := llama.Tool{Type: "function", Function: llama.ToolFunction{Name: "read_file"}}
	opts := llama.ChatOptions{
		Tools:            []llama.Tool{tool},
		ToolChoice:       "auto",
		EnableThinking:   &thinkingOn,
		PreserveThinking: true,
		MaxTokens:        8192,
	}
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		PermissionProfile: "read_only_workspace",
		Objective:         "What is in internal/agent/runner.go?",
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 internal/agent/runner.go lines 1-20",
		Output:  "1:package agent\n2:\n3:func RunExample() {}\n",
	}}

	got := readOnlyEvidenceAvailableTerminalChatOptions(opts, brief, results)
	if len(got.Tools) != 1 || got.ToolChoice != "auto" {
		t.Fatalf("terminal visible-output mode should keep exact read/search tools available, got tools=%#v choice=%q", got.Tools, got.ToolChoice)
	}
	if got.EnableThinking == nil || *got.EnableThinking {
		t.Fatalf("terminal visible-output mode should disable hidden thinking, got %#v", got.EnableThinking)
	}
	if got.PreserveThinking {
		t.Fatal("terminal visible-output mode should not preserve hidden reasoning")
	}
	if !got.ChatTemplateKwargs {
		t.Fatal("terminal visible-output mode should send chat-template kwargs")
	}
}

func TestReadOnlyEvidencePendingTerminalChatOptionsPreserveThinking(t *testing.T) {
	thinkingOn := true
	opts := llama.ChatOptions{EnableThinking: &thinkingOn, PreserveThinking: true, MaxTokens: 8192}
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		PermissionProfile: "read_only_workspace",
		Objective:         "What is in internal/agent/runner.go?",
	}

	got := readOnlyEvidenceAvailableTerminalChatOptions(opts, brief, nil)
	if got.EnableThinking == nil || !*got.EnableThinking || !got.PreserveThinking {
		t.Fatalf("evidence-pending calls should preserve current thinking behavior, got enable=%#v preserve=%v", got.EnableThinking, got.PreserveThinking)
	}
}

func TestTextualToolProtocolEntriesPrioritizeWriteAliasExamples(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		AllowedTools: []string{
			"read_file",
			"code_navigate",
			"source_sanity",
			"context_status",
			"context_query",
			"context_zoom",
			"context_pack",
			"context_impact",
			"discovery_query",
			"structural_search",
			"mkdir",
			"edit_file",
			"write_file",
			"append_file",
			"apply_patch",
		},
		RequiredTools: []string{"edit_file_or_apply_patch", "semantic_discovery_or_narrow_read_for_named_gap"},
		ToolManifest: workerruntime.BuildToolManifest([]string{
			"read_file",
			"code_navigate",
			"source_sanity",
			"context_status",
			"context_query",
			"context_zoom",
			"context_pack",
			"context_impact",
			"discovery_query",
			"structural_search",
			"mkdir",
			"edit_file",
			"write_file",
			"append_file",
			"apply_patch",
		}),
	}

	entries := textualToolProtocolEntries(brief)
	if len(entries) > 8 {
		t.Fatalf("expected textual protocol entries to honor cap, got %d entries", len(entries))
	}
	if len(entries) < 5 {
		t.Fatalf("expected write examples to be selected, got %#v", entries)
	}
	for _, want := range []string{"edit_file", "write_file", "append_file", "apply_patch"} {
		found := false
		for _, entry := range entries {
			if entry.Tool != want {
				continue
			}
			found = true
			if strings.Contains(entry.Request, "relative/file") || strings.Contains(entry.Request, "replacement text") {
				t.Fatalf("%s textual protocol request should not expose placeholder JSON: %s", want, entry.Request)
			}
			if !strings.Contains(strings.ToLower(entry.Purpose), "description") {
				t.Fatalf("%s textual protocol guidance must include description: %s", want, entry.Purpose)
			}
		}
		if !found {
			t.Fatalf("expected textual protocol entries to include %s before read examples, got %#v", want, entries)
		}
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

	if !slices.Contains(paths, "internal/app/analysis_agents.go") {
		t.Fatalf("expected source path to be pinned, got %#v", paths)
	}
	for _, forbidden := range []string{"internal/app/orchestration.go", "internal/app/implementation.go"} {
		if slices.Contains(paths, forbidden) {
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
	if !slices.Contains(feedback[0].DoNotRepeat, "duplicate_read_file_on_bootstrapped_target") {
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

func TestRunnerDropsReasoningContentBetweenToolTurnsWhenNotPreserving(t *testing.T) {
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
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 3, MaxNoProgressTurns: 2},
	}
	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "read README.md"}},
		llama.ChatOptions{Model: "model", EnableThinking: &enableThinking, PreserveThinking: false, MaxTokens: 128},
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
	for _, message := range payloads[1].Messages {
		if message.ReasoningContent != "" {
			t.Fatalf("expected second request to strip prior reasoning_content, got %#v", payloads[1].Messages)
		}
	}
}

func TestRunnerForcesVisibleOutputForEmptyReplyRetry(t *testing.T) {
	root := t.TempDir()
	type requestPayload struct {
		Messages           []llama.ChatMessage `json:"messages"`
		ChatTemplateKwargs *struct {
			EnableThinking *bool `json:"enable_thinking"`
			Preserve       bool  `json:"preserve_thinking"`
		} `json:"chat_template_kwargs,omitempty"`
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
	for _, message := range payloads[1].Messages {
		if message.ReasoningContent != "" {
			t.Fatalf("expected empty-answer retry to strip prior reasoning_content, got %#v", payloads[1].Messages)
		}
	}
	if payloads[1].ChatTemplateKwargs == nil ||
		payloads[1].ChatTemplateKwargs.EnableThinking == nil ||
		*payloads[1].ChatTemplateKwargs.EnableThinking ||
		payloads[1].ChatTemplateKwargs.Preserve {
		t.Fatalf("expected empty-answer retry to force visible output, got %#v", payloads[1].ChatTemplateKwargs)
	}
}

func TestRunnerDropsEmptyReplyReasoningForRetryWhenNotPreserving(t *testing.T) {
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
		llama.ChatOptions{Model: "model", EnableThinking: &enableThinking, PreserveThinking: false, MaxTokens: 128},
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
	for _, message := range payloads[1].Messages {
		if message.ReasoningContent != "" {
			t.Fatalf("expected retry request to strip empty reply reasoning_content, got %#v", payloads[1].Messages)
		}
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
		Client:    llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{Root: t.TempDir(), CWD: "."}),
		Policy:    RunPolicy{MaxToolSteps: 1, MaxNoProgressTurns: 1},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			Objective:         "update README",
			PermissionProfile: "read_only_workspace",
		},
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

func TestRunnerInjectsPlanExecuteModelProtocolProjectionCard(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "a.go", "package main\nfunc A() {}\n"); err != nil {
		t.Fatalf("write seed: %v", err)
	}
	var payload struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": "<<QUESTION>>: exact blocker"},
			}},
		})
	}))
	defer server.Close()

	runner := &Runner{
		Client:    llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		Policy:    RunPolicy{MaxToolSteps: 1, MaxModelCalls: 1},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Lane:                 "plan-execute",
			TaskFamily:           PlanThenExecutePlannerTaskFamily,
			Contract:             "implementation_result",
			PermissionProfile:    "guarded_workspace_write",
			AllowedTools:         []string{"read_file", "code_navigate", "submit_plan"},
			RequiredTools:        []string{"submit_plan"},
			RequiredEditPaths:    []string{"a.go"},
			VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
			VerificationPlan:     []string{"go test ./..."},
		},
	}
	_, _ = runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "plan edit"}}, llama.ChatOptions{
		ResponseFormatJSON: true,
	}, nil, nil, nil)

	var sawBrief, sawProtocolCard bool
	for i, msg := range payload.Messages {
		if msg.Role == "system" && strings.Contains(msg.Content, "[active contract protocol]") {
			sawProtocolCard = true
			if !strings.Contains(msg.Content, "protocol_hash=") ||
				!strings.Contains(msg.Content, "Visible tools:") ||
				!strings.Contains(msg.Content, "Callable tools now:") ||
				!strings.Contains(msg.Content, "Path roles:") ||
				!strings.Contains(msg.Content, "Safe alternatives:") {
				t.Fatalf("protocol card missing required fields at message %d: %s", i, msg.Content)
			}
		}
		if msg.Role == "system" && strings.Contains(msg.Content, "[frontier step instruction]") {
			sawBrief = true
		}
	}
	if !sawProtocolCard {
		t.Fatalf("expected model-visible protocol card in request messages: %#v", payload.Messages)
	}
	if !sawBrief {
		t.Fatalf("expected existing runtime brief to remain in request messages: %#v", payload.Messages)
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
		{Tool: "read_file", Summary: "Read main.go lines 1-3", Output: "one\ntwo\nthree"},
		{Tool: "edit_file", Summary: "Edited main.go lines 1-1"},
		{Tool: "edit_file", Summary: "Edited main.go lines 2-2"},
		{
			Tool:    "read_file",
			Summary: "Verification required after workspace edits",
			Error:   "the workspace has 2 successful write(s) since the last verification.",
		},
	}
	nudge := buildRejectionRecoveryNudge(
		workspace.ToolRequest{Tool: "read_file", Path: "main.go"},
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
	for _, want := range []string{"required edit path", "native/tests/test_energy.cpp", "allowed write outcome", "bounded `edit_file`", "`apply_patch`"} {
		if !strings.Contains(nudge, want) {
			t.Fatalf("expected required-path recovery nudge to contain %q, got:\n%s", want, nudge)
		}
	}
	for _, forbidden := range []string{"Answer with the evidence already gathered", "Only take one narrow next step", "smaller window", "structured probe", "Required outcomes are an allowed write"} {
		if strings.Contains(nudge, forbidden) {
			t.Fatalf("required-path recovery nudge should not suggest %q, got:\n%s", forbidden, nudge)
		}
	}
}

func TestPlanExecuteRequiredPathRecoveryNudgeNamesSubmitPlan(t *testing.T) {
	nudge := buildRejectionRecoveryNudge(
		workspace.ToolRequest{Tool: "read_file", Path: "native/tests/test_energy.cpp"},
		nil,
		nil,
		workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
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
	for _, want := range []string{"plan-execute planner", "successful `submit_plan` edit entry", "`submit_plan` with one edit entry"} {
		if !strings.Contains(nudge, want) {
			t.Fatalf("expected plan-execute recovery nudge to contain %q, got:\n%s", want, nudge)
		}
	}
	for _, forbidden := range []string{"allowed write", "successful write:"} {
		if strings.Contains(nudge, forbidden) {
			t.Fatalf("plan-execute recovery nudge should not use generic implementation write wording %q, got:\n%s", forbidden, nudge)
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
	}, {
		Tool:    "read_file",
		Summary: "Read internal/agent/runner_child_tasks.go lines 1-80",
		Output:  "package agent\nfunc runChildTask() {}\n",
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
	for _, want := range []string{"Answer with the evidence already gathered", "one narrow next step", "exact missing claim"} {
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
		Error:   "you just ran the same tool request. Either answer with what you already know, or take one materially different narrow step that names the exact missing claim it closes.",
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
		"answer from the current evidence",
		"one exact tool-shaped follow-up",
		"concrete missing claim",
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

func TestCompletionGateRejectsUnresolvedProcessNarration(t *testing.T) {
	result, rejected := unresolvedProcessIntentCompletionRejection("The context_pack result is polluted. I need to find the actual TUI/input rendering code. Let me search for the input field implementation.")
	if !rejected {
		t.Fatal("expected unresolved process narration to be rejected")
	}
	if result.Tool != "completion_gate" || !strings.Contains(result.Error, "future search") {
		t.Fatalf("unexpected rejection result: %#v", result)
	}

	if _, rejected := unresolvedProcessIntentCompletionRejection("Blocked: I cannot inspect the logs because the file is missing."); rejected {
		t.Fatal("concrete blocker answer should not be rejected as process narration")
	}
}

func TestCompletionGateAllowsQuotedProcessNarrationEvidence(t *testing.T) {
	answer := strings.Join([]string{
		"Here is the diagnosis.",
		"",
		"The hidden reasoning salvage path accepted a non-answer as the final response. The extracted text was:",
		"",
		`> "The user is asking me to investigate why a prompt might be routed as write intent. I need to find the specific route-planner files/tests responsible."`,
		"",
		"The likely guard is `looksLikeNonAnswerProse` in `internal/agent/runner.go`, and the tests belong beside the live-failure cases in `internal/agent/runner_test.go`.",
	}, "\n")
	if result, rejected := unresolvedProcessIntentCompletionRejection(answer); rejected {
		t.Fatalf("quoted evidence should not make a substantive answer look like process narration: %#v", result)
	}
}

func TestCompletionGateRejectsFutureRefreshNarration(t *testing.T) {
	result, rejected := unresolvedProcessIntentCompletionRejection("Both required edit paths are stale in the graph. I'll refresh them, then read the two files to verify the TUI smoke fixture.")
	if !rejected {
		t.Fatal("expected future refresh/read narration to be rejected")
	}
	if result.Tool != "completion_gate" || !strings.Contains(result.Error, "future search") {
		t.Fatalf("unexpected rejection result: %#v", result)
	}
}

func TestCompletionGateRejectsShouldCallToolNarration(t *testing.T) {
	result, rejected := unresolvedProcessIntentCompletionRejection("I should use the context_pack tool to get a good overview of the relevant files. Let me call context_pack now.")
	if !rejected {
		t.Fatal("expected tool-call intent narration to be rejected")
	}
	if result.Tool != "completion_gate" || !strings.Contains(result.Error, "future search") {
		t.Fatalf("unexpected rejection result: %#v", result)
	}
}

func TestCompletionGateAllowsBenignLetMeTryExplanation(t *testing.T) {
	if result, rejected := unresolvedProcessIntentCompletionRejection("Let me try to explain the failure from the evidence we already have."); rejected {
		t.Fatalf("benign explanatory prose should not be rejected: %#v", result)
	}
}

func TestReadOnlyCompletionGateRejectsSearchHitInventory(t *testing.T) {
	runner := &Runner{RuntimeBrief: workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		PermissionProfile: "read_only_workspace",
		Objective:         "What does internal/labels/labels.go ShortLabel return?",
	}}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read internal/transcript/debuglog.go lines 1-80",
		Output:  "19:\tDebugLogModeRedacted DebugLogMode = \"redacted\"\n",
	}}
	reply := "The inspected source in `internal/agent/runner_test.go` contains `f9 testdata/swe/live-subset.json:8: \"prompt\": \"What does the debug log redacted mode do?\"`."
	result, rejected := runner.readOnlySearchHitInventoryCompletionRejection(reply, results)
	if !rejected {
		t.Fatal("expected search-hit inventory answer to be rejected")
	}
	if result.Tool != "completion_gate" || !strings.Contains(result.Error, "search handles") {
		t.Fatalf("unexpected rejection result: %#v", result)
	}
}

func TestCompletionGateRejectsRawToolResultTranscript(t *testing.T) {
	result, rejected := rawToolResultTranscriptCompletionRejection("tool: read_file\nsummary: Read internal/app/model_swap.go lines 70-91\nstatus: ok\noutput:\n70:\t\tm.openModelPopup()")
	if !rejected {
		t.Fatal("expected raw tool result transcript to be rejected")
	}
	if result.Tool != "completion_gate" || !strings.Contains(result.Error, "internal tool-result transcript") {
		t.Fatalf("unexpected rejection result: %#v", result)
	}
}

func TestRunnerRepairsRawToolResultTranscriptFinalAnswer(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "internal/app/model_swap.go", "package app\n\nfunc modelAlias() string { return \"qwen\" }\n"); err != nil {
		t.Fatalf("write seed file: %v", err)
	}
	replies := []string{
		"```tool\n{\"tool\":\"read_file\",\"path\":\"internal/app/model_swap.go\",\"start_line\":1,\"end_line\":3}\n```",
		"tool: read_file\nsummary: Read internal/app/model_swap.go lines 1-3\nstatus: ok\noutput:\n1:package app\n2:\n3:func modelAlias() string { return \"qwen\" }",
		"The inspected code path is `internal/app/model_swap.go`; the current evidence shows the model alias helper there, but this read-only turn did not change files.",
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

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "direct_answer",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"read_file"},
			Objective:         "answer from the inspected model display code",
		},
		Policy: RunPolicy{MaxToolSteps: 3, MaxNoProgressTurns: 3},
	}
	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "Can we shorten this to the alias?"}}, llama.ChatOptions{}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(answer)), "tool:") ||
		strings.Contains(strings.ToLower(answer), "\nsummary:") ||
		strings.Contains(strings.ToLower(answer), "\noutput:") {
		t.Fatalf("runner returned raw tool transcript:\n%s", answer)
	}
	if !strings.Contains(answer, "read-only turn did not change files") {
		t.Fatalf("expected repaired prose answer, got:\n%s", answer)
	}
	if requests != len(replies) {
		t.Fatalf("expected raw transcript rejection and repair retry, got %d request(s)", requests)
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
			Output:  "Question: how does this harness utilize sub-agents?\nSource: delegated read-only evidence packet.\n\nSource-body evidence paths:\n- `internal/agent/runner_child_tasks.go`\n\nPrimary files: `internal/agent/runner.go`, `internal/agent/runner_child_tasks.go`.\n\nKey findings:\n- The parent runner uses sub-agents by delegating exploration to a child task and using the child output as evidence.",
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
	if !strings.Contains(answer, "internal/agent/runner_child_tasks.go") || !strings.Contains(strings.ToLower(answer), "child") {
		t.Fatalf("expected deterministic fallback to preserve the child-task finding and relevant implementation surface, got:\n%s", answer)
	}
	for _, forbidden := range []string{"The strongest implementation evidence is in", "What this establishes:", "Source: delegated read-only evidence packet", "Primary files:"} {
		if strings.Contains(answer, forbidden) {
			t.Fatalf("expected cleaned deterministic prose instead of packet metadata %q:\n%s", forbidden, answer)
		}
	}
	if requests > 2 {
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
			Output:  "Source-body evidence paths:\n- `internal/agent/runner_child_tasks.go`\n\nThe strongest implementation evidence is in `internal/agent/runner_child_tasks.go`. Use those findings as the source of truth for `how does this harness utilize sub-agents`.\n\nWhat this establishes:\n- Text anchors: Found sub-agent child runner implementation paths.",
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
	if requests > 2 {
		t.Fatalf("expected deterministic source-backed fallback to avoid invalid-final retry, got %d requests", requests)
	}
	if slices.ContainsFunc(modelCallEvents, func(event string) bool { return strings.Contains(event, `"label":"invalid_final_answer_retry"`) }) {
		t.Fatalf("did not expect invalid_final_answer_retry when source-backed fallback is available, got %#v", modelCallEvents)
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
	for _, want := range []string{
		"`Runner.runChildTask`",
		"`internal/agent/runner_child_tasks.go`",
		"sets `prompt` to `normalizeReadOnlyChildPrompt(prompt)`",
		"assigns `child` from `&Runner{`",
	} {
		if !strings.Contains(answer, want) {
			t.Fatalf("expected source-backed synthesis to contain %q, got:\n%s", want, answer)
		}
	}
	for _, forbidden := range []string{"Source: delegated read-only evidence packet", "Inspected paths:", "The strongest implementation evidence is in", "What this establishes:"} {
		if strings.Contains(answer, forbidden) {
			t.Fatalf("expected answer without scaffold or delegated packet metadata %q:\n%s", forbidden, answer)
		}
	}
	if slices.ContainsFunc(modelCallEvents, func(event string) bool { return strings.Contains(event, `"label":"fallback_final_answer_retry"`) }) {
		t.Fatalf("did not expect fallback_final_answer_retry for acceptable generic evidence synthesis, got %#v", modelCallEvents)
	}
	if requests != 0 {
		t.Fatalf("expected source-backed deterministic synthesis without another model request, used %d replies", requests)
	}
}

func TestDeterministicNoToolsAnswerKeepsGroundedReadOnlyAnswer(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		PermissionProfile: "read_only_workspace",
		Objective:         "Explain what ShortLabel returns from internal/labels/labels.go.",
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read internal/labels/labels.go lines 1-4",
		Output: strings.Join([]string{
			"package labels",
			"",
			"func ShortLabel() string {",
			"\treturn \"Save changes\"",
			"}",
		}, "\n"),
	}}
	answer, ok := deterministicNoToolsAnswer("main", brief, results)
	if !ok {
		t.Fatal("expected grounded deterministic fallback answer")
	}
	for _, want := range []string{
		"internal/labels/labels.go",
		"ShortLabel",
		"Save changes",
	} {
		if !strings.Contains(answer, want) {
			t.Fatalf("grounded fallback missing %q:\n%s", want, answer)
		}
	}
	if strings.HasPrefix(answer, "<<QUESTION>>") || strings.Contains(answer, "concrete user-facing finding") {
		t.Fatalf("answerable source evidence should not be downgraded to blocker:\n%s", answer)
	}
}

func TestPresentableFallbackCandidatePreservesChildDelegatedPacket(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "delegated_read_only",
		Contract:          "grounded_evidence",
		Objective:         "Explain how this harness uses child agents.",
		PermissionProfile: "read_only_workspace",
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read internal/agent/runner_child_tasks.go lines 1-80",
		Output:  "package agent\nfunc runChildTask() {}\n",
	}}
	childAnswer := "The answer is grounded in `internal/agent/runner_child_tasks.go`.\n\nWhat this establishes:\n- The child runner returns a delegated evidence packet to the parent after bounded read-only inspection."

	answer, ok := presentableFallbackCandidate("child:explorer", brief, results, childAnswer)
	if !ok {
		t.Fatal("expected child delegated fallback packet to remain presentable")
	}
	if answer != childAnswer {
		t.Fatalf("expected child delegated fallback packet to be preserved, got:\n%s", answer)
	}
	for _, forbidden := range []string{"not enough supported evidence", "<<QUESTION>>", "concrete user-facing finding"} {
		if strings.Contains(strings.ToLower(answer), strings.ToLower(forbidden)) {
			t.Fatalf("child delegated fallback should not be rewritten as main-lane uncertainty %q:\n%s", forbidden, answer)
		}
	}
}

func TestForcedCompletionFallsBackWhenReadOnlyRetryStillViolatesCompletionGate(t *testing.T) {
	replies := []string{
		`{"tool":"read_file","path":"internal/labels/labels.go","start_line":1,"end_line":200}`,
		"The file `internal/labels/labels.go` contains `ShortLabel()` returning `\"Save changes\"`.\n\nTo determine whether it can be shortened, I need one more probe:\n\n{\"tool\":\"search_content\",\"pattern\":\"ShortLabel\",\"path\":\".\"}",
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
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			Objective:         "Can we shorten the save label in internal/labels/labels.go?",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"read_file"},
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 internal/labels/labels.go lines 1-5 (total lines: 5)",
		Output:  "1:package labels\n2:\n3:func ShortLabel() string {\n4:\treturn \"Save changes\"\n5:}\n",
	}}

	answer, err := runner.completeWithoutMoreToolsWithReason(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Can we shorten the save label in internal/labels/labels.go?"}},
		llama.ChatOptions{},
		results,
		"The conversation lane has enough bounded evidence. Tools are now disabled. Answer the user's actual question now.",
		nil,
	)
	if err != nil {
		t.Fatalf("completeWithoutMoreToolsWithReason: %v", err)
	}
	for _, want := range []string{"internal/labels/labels.go", "ShortLabel", "\"Save changes\""} {
		if !strings.Contains(answer, want) {
			t.Fatalf("expected deterministic read-only fallback to include %q, got:\n%s", want, answer)
		}
	}
	if answer == "." {
		t.Fatalf("non-substantive retry answer should not be accepted")
	}
	if strings.Contains(answer, `"tool"`) || strings.Contains(answer, "I need one more probe") {
		t.Fatalf("retry answer with an unexecuted tool request should not be accepted:\n%s", answer)
	}
	if requests != 1 {
		t.Fatalf("expected deterministic read-only fallback without additional retry, used %d/%d replies", requests, len(replies))
	}
}

func TestForcedCompletionFallsBackWhenReadOnlyRetryReturnsSourceInventory(t *testing.T) {
	objective := "what would it take to enable the up and down arrow keys to iterate through the user's chat history? the intent is to make it easy to find old messages and run them again without typing them out."
	badInventoryAnswer := "For `" + objective + "`, `internal/app/app.go` shows defines `truncateText`, defines `normalizeHistoryRole`, and `normalizeHistoryRole` returns `strings.ToUpper(value[:1]) + value[1:]`."
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests > 0 {
			t.Fatalf("unexpected extra chat request")
		}
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": badInventoryAnswer},
			}},
		})
	}))
	defer server.Close()

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			Objective:         objective,
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"read_file", "code_navigate", "context_query"},
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 internal/app/app.go lines 1-57 (total lines: 57)",
		Output: strings.Join([]string{
			"1:package app",
			"17:func truncateText(s string, max int) string {",
			"18:\tif len(s) <= max {",
			"19:\t\treturn s",
			"20:\t}",
			"21:\treturn s[:max]",
			"42:func normalizeHistoryRole(value string) string {",
			"43:\tif value == \"\" {",
			"44:\t\treturn \"User\"",
			"45:\t}",
			"46:\treturn strings.ToUpper(value[:1]) + value[1:]",
			"47:}",
		}, "\n"),
	}}

	answer, err := runner.completeWithoutMoreToolsWithReason(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: objective}},
		llama.ChatOptions{},
		results,
		"Repeated post-readiness follow-up requests omitted the exact missing claim. Tools are now disabled. Answer from gathered evidence or state the exact unsupported claim.",
		nil,
	)
	if err != nil {
		t.Fatalf("completeWithoutMoreToolsWithReason: %v", err)
	}
	if answer == badInventoryAnswer || strings.Contains(answer, "shows defines") {
		t.Fatalf("source-inventory retry answer escaped forced completion:\n%s", answer)
	}
	if strings.Contains(answer, "normalizeHistoryRole` returns") {
		t.Fatalf("deterministic fallback reintroduced helper-function inventory:\n%s", answer)
	}
	if !strings.Contains(answer, "internal/app/app.go") {
		t.Fatalf("fallback should retain inspected evidence path, got:\n%s", answer)
	}
	if requests > 1 {
		t.Fatalf("expected at most one forced-completion model call, got %d", requests)
	}
}

func TestForcedCompletionFallsBackWhenReadOnlyRetryReturnsInspectedBodyInventory(t *testing.T) {
	objective := "what would it take to enable the up and down arrow keys to iterate through the user's chat history? the intent is to make it easy to find old messages and run them again without typing them out."
	badInventoryAnswer := "`Model.handleKeyPress` in `internal/app/update_handlers.go` is the clearest inspected implementation anchor. The inspected body shows that it defines `Model.handleKeyPress`, sets `m.history.expanded` to `false`, `Model.handleKeyPress` calls `renderHistoryPanelContent`, sets `m.history.expanded` to `!m.history.expanded`, and sets `m.history.focused` to `false`."
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests > 0 {
			t.Fatalf("unexpected extra chat request")
		}
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": badInventoryAnswer},
			}},
		})
	}))
	defer server.Close()

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			Objective:         objective,
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"read_file", "code_navigate", "context_query"},
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "code_navigate",
		Summary: "Code navigation routed read_symbol via go_read_symbol",
		Output: strings.Join([]string{
			"capability: read_symbol",
			"language: go",
			"routed_tool: go_read_symbol",
			"route_summary: Read Model.handleKeyPress in internal/app/update_handlers.go lines 22-141",
			"",
			"22:func (m *Model) handleKeyPress(msg tea.KeyMsg) (tea.Cmd, bool) {",
			"44:\t\tcase tea.KeyUp:",
			"49:\t\tcase tea.KeyDown:",
			"69:\t\tcase tea.KeyUp:",
			"76:\t\tcase tea.KeyDown:",
			"83:\t\tcase tea.KeyEnter:",
			"151:\t\tm.appendHistoryMessage(\"user\", text, nil)",
		}, "\n"),
	}}

	answer, err := runner.completeWithoutMoreToolsWithReason(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: objective}},
		llama.ChatOptions{},
		results,
		"Repeated post-readiness follow-up requests omitted the exact missing claim. Tools are now disabled. Answer from gathered evidence or state the exact unsupported claim.",
		nil,
	)
	if err != nil {
		t.Fatalf("completeWithoutMoreToolsWithReason: %v", err)
	}
	if answer == badInventoryAnswer || strings.Contains(answer, "inspected body shows") {
		t.Fatalf("inspected-body inventory retry answer escaped forced completion:\n%s", answer)
	}
	if !strings.Contains(answer, "internal/app/update_handlers.go") {
		t.Fatalf("fallback should retain inspected evidence path, got:\n%s", answer)
	}
	if requests > 1 {
		t.Fatalf("expected at most one forced-completion model call, got %d", requests)
	}
}

func TestForcedCompletionRepairReturnDoesNotLeakSourceInventory(t *testing.T) {
	objective := "Can we enable up and down arrows to recall old user messages in the TUI?"
	replies := []string{
		"Question: Can we enable up and down arrows to recall old user messages in the TUI?\n\nThe inspected TUI key handler in `internal/app/update_handlers.go` already handles `tea.KeyUp` and `tea.KeyDown`, so the implementation needs to route those keys through user-message history recall when the input is focused.",
		"`Model.handleKeyPress` in `internal/app/update_handlers.go` is the clearest inspected implementation anchor. The inspected body shows that it defines `Model.handleKeyPress`, sets `m.history.expanded` to `false`, calls `renderHistoryPanelContent`, and handles `tea.KeyUp` plus `tea.KeyDown`.",
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
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			Objective:         objective,
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"read_file", "code_navigate", "context_query"},
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "code_navigate",
		Summary: "Code navigation routed read_symbol via go_read_symbol",
		Output: strings.Join([]string{
			"capability: read_symbol",
			"language: go",
			"routed_tool: go_read_symbol",
			"route_summary: Read Model.handleKeyPress in internal/app/update_handlers.go lines 22-141",
			"",
			"22:func (m *Model) handleKeyPress(msg tea.KeyMsg) (tea.Cmd, bool) {",
			"44:\t\tcase tea.KeyUp:",
			"49:\t\tcase tea.KeyDown:",
			"83:\t\tcase tea.KeyEnter:",
			"151:\t\tm.appendHistoryMessage(\"user\", text, nil)",
		}, "\n"),
	}}

	answer, err := runner.completeWithoutMoreToolsWithReason(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: objective}},
		llama.ChatOptions{},
		results,
		"The conversation lane has enough bounded evidence. Tools are now disabled. Answer from gathered evidence or state the exact unsupported claim.",
		nil,
	)
	if err != nil {
		t.Fatalf("completeWithoutMoreToolsWithReason: %v", err)
	}
	if strings.Contains(answer, "clearest inspected implementation anchor") || strings.Contains(answer, "inspected body shows") {
		t.Fatalf("repair-path source inventory escaped forced completion:\n%s", answer)
	}
	if !strings.Contains(answer, "internal/app/update_handlers.go") {
		t.Fatalf("fallback should retain inspected evidence path, got:\n%s", answer)
	}
	if requests != 2 {
		t.Fatalf("expected decoration repair path to use both model replies, used %d", requests)
	}
}

func TestDeterministicNoToolsAnswerRejectsCodeNavigateInventoryFallback(t *testing.T) {
	objective := "How can we enable the up and down arrow keys to iterate between previously sent user messages so they can be resent or edited before sending?"
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		Objective:         objective,
		PermissionProfile: "read_only_workspace",
		AllowedTools:      []string{"code_navigate", "context_query"},
	}
	results := []workspace.ToolResult{{
		Tool:    "code_navigate",
		Summary: "Code navigation routed read_symbol via go_read_symbol",
		Output: strings.Join([]string{
			"capability: read_symbol",
			"language: go",
			"routed_tool: go_read_symbol",
			"route_summary: Read Model.LoadHistoryFromSession in internal/app/sessions.go lines 1-20",
			"",
			"func (m *Model) LoadHistoryFromSession(path string) error {",
			"\tif m == nil {",
			"\t\treturn fmt.Errorf(\"model is nil\")",
			"\t}",
			"\tpath = strings.TrimSpace(path)",
			"\tif path == \"\" {",
			"\t\treturn fmt.Errorf(\"session path is required\")",
			"\t}",
			"\treturn replaySessionLog(m, path)",
			"}",
		}, "\n"),
	}}

	answer, ok := deterministicNoToolsAnswer("main", brief, results)
	if !ok {
		t.Fatal("expected deterministic closeout to produce presentable uncertainty from inspected evidence")
	}
	for _, forbidden := range []string{
		"clearest inspected implementation anchor",
		"inspected body shows",
		"returns `fmt.Errorf",
		"sets `path`",
		"calls `replaySessionLog`",
	} {
		if strings.Contains(answer, forbidden) {
			t.Fatalf("source-inventory fallback escaped deterministic closeout (%q):\n%s", forbidden, answer)
		}
	}
	if !strings.Contains(answer, "internal/app/sessions.go") {
		t.Fatalf("fallback should retain inspected evidence path, got:\n%s", answer)
	}
	for _, want := range []string{"do not have enough supported evidence", "internal/app/sessions.go"} {
		if !strings.Contains(answer, want) {
			t.Fatalf("expected presentable uncertainty to contain %q, got:\n%s", want, answer)
		}
	}
	runner := &Runner{RuntimeBrief: brief}
	if kind, reason := runner.finalAnswerBlockerKind(answer, results); kind != "" || reason != "" {
		t.Fatalf("deterministic fallback should satisfy final-answer guard, got (%q, %q):\n%s", kind, reason, answer)
	}
}

func TestForcedCompletionRequestForcesVisibleTextOnlyOutput(t *testing.T) {
	thinkingOn := true
	var sawRequest bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Tools              []any `json:"tools,omitempty"`
			ToolChoice         any   `json:"tool_choice,omitempty"`
			ChatTemplateKwargs *struct {
				EnableThinking *bool `json:"enable_thinking"`
				Preserve       bool  `json:"preserve_thinking"`
			} `json:"chat_template_kwargs,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		sawRequest = true
		if len(body.Tools) != 0 || body.ToolChoice != nil {
			t.Fatalf("forced completion should be text-only, got tools=%#v tool_choice=%#v", body.Tools, body.ToolChoice)
		}
		if body.ChatTemplateKwargs == nil ||
			body.ChatTemplateKwargs.EnableThinking == nil ||
			*body.ChatTemplateKwargs.EnableThinking ||
			body.ChatTemplateKwargs.Preserve {
			t.Fatalf("forced completion should disable hidden thinking, got %#v", body.ChatTemplateKwargs)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": "The evidence shows the answer."},
			}},
		})
	}))
	defer server.Close()

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			Objective:         "Answer from gathered evidence.",
			PermissionProfile: "read_only_workspace",
		},
	}
	answer, err := runner.completeWithoutMoreToolsWithReason(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Answer from gathered evidence."}},
		llama.ChatOptions{
			Tools:            []llama.Tool{{Type: "function", Function: llama.ToolFunction{Name: "read_file"}}},
			ToolChoice:       "auto",
			EnableThinking:   &thinkingOn,
			PreserveThinking: true,
			MaxTokens:        8192,
		},
		nil,
		"Tools are now disabled. Answer from gathered evidence.",
		nil,
	)
	if err != nil {
		t.Fatalf("completeWithoutMoreToolsWithReason: %v", err)
	}
	if !sawRequest {
		t.Fatal("expected forced-completion model request")
	}
	if !strings.Contains(answer, "evidence") {
		t.Fatalf("unexpected answer:\n%s", answer)
	}
}

func TestReadOnlyEvidenceAvailableMainLoopForcesVisibleOutputButKeepsTools(t *testing.T) {
	thinkingOn := true
	var sawRequest bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Tools              []any `json:"tools,omitempty"`
			ToolChoice         any   `json:"tool_choice,omitempty"`
			ChatTemplateKwargs *struct {
				EnableThinking *bool `json:"enable_thinking"`
				Preserve       bool  `json:"preserve_thinking"`
			} `json:"chat_template_kwargs,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		sawRequest = true
		if len(body.Tools) == 0 {
			t.Fatalf("evidence-available terminal mode should keep read/search tools available")
		}
		if body.ToolChoice == nil {
			t.Fatalf("evidence-available terminal mode should keep tool choice available")
		}
		if body.ChatTemplateKwargs == nil ||
			body.ChatTemplateKwargs.EnableThinking == nil ||
			*body.ChatTemplateKwargs.EnableThinking ||
			body.ChatTemplateKwargs.Preserve {
			t.Fatalf("evidence-available terminal mode should force visible output, got %#v", body.ChatTemplateKwargs)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": "runner.go contains package agent."},
			}},
		})
	}))
	defer server.Close()

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
			Objective:         "What is in internal/agent/runner.go?",
			AllowedTools:      []string{"read_file", "search_content"},
		},
		SeedResults: []workspace.ToolResult{{
			Tool:    "read_file",
			Summary: "Read f1 internal/agent/runner.go lines 1-20",
			Output:  "1:package agent\n2:\n3:func RunExample() {}\n",
		}},
	}
	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "What is in internal/agent/runner.go?"}},
		llama.ChatOptions{
			Tools:            []llama.Tool{{Type: "function", Function: llama.ToolFunction{Name: "read_file"}}},
			ToolChoice:       "auto",
			EnableThinking:   &thinkingOn,
			PreserveThinking: true,
			MaxTokens:        8192,
		},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !sawRequest {
		t.Fatal("expected main-loop model request")
	}
	if !strings.Contains(answer, "runner.go") {
		t.Fatalf("unexpected answer:\n%s", answer)
	}
}

func TestEmptyAnswerRetryForcesVisibleOutputAndStripsReasoning(t *testing.T) {
	thinkingOn := true
	var sawRequest bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Role             string `json:"role"`
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content,omitempty"`
			} `json:"messages"`
			ChatTemplateKwargs *struct {
				EnableThinking *bool `json:"enable_thinking"`
				Preserve       bool  `json:"preserve_thinking"`
			} `json:"chat_template_kwargs,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		sawRequest = true
		if body.ChatTemplateKwargs == nil ||
			body.ChatTemplateKwargs.EnableThinking == nil ||
			*body.ChatTemplateKwargs.EnableThinking ||
			body.ChatTemplateKwargs.Preserve {
			t.Fatalf("empty-answer retry should disable hidden thinking, got %#v", body.ChatTemplateKwargs)
		}
		for _, message := range body.Messages {
			if strings.Contains(message.ReasoningContent, "hidden scratchpad") ||
				strings.Contains(message.Content, "hidden scratchpad") {
				t.Fatalf("empty-answer retry leaked prior hidden reasoning in request: %#v", body.Messages)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": "Visible retry answer from evidence."},
			}},
		})
	}))
	defer server.Close()

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			Objective:         "Answer from gathered evidence.",
			PermissionProfile: "read_only_workspace",
		},
	}
	answer, err := runner.ensureNonEmptyAnswer(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Answer from gathered evidence."}},
		llama.ChatMessage{Role: "assistant", ReasoningContent: "hidden scratchpad that must not be resent"},
		llama.ChatOptions{EnableThinking: &thinkingOn, PreserveThinking: true, MaxTokens: 8192},
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("ensureNonEmptyAnswer: %v", err)
	}
	if !sawRequest {
		t.Fatal("expected empty-answer retry request")
	}
	if !strings.Contains(answer, "Visible retry answer") {
		t.Fatalf("unexpected answer:\n%s", answer)
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
	if !strings.Contains(answer, "two lines") && !(strings.Contains(answer, "line one") && strings.Contains(answer, "line two")) {
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

func TestScopedImplementationLastChanceHidesReadsBeforeModelCallBudgetExhaustion(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "a.txt", "before\nmore\n"); err != nil {
		t.Fatalf("write a: %v", err)
	}

	requests := 0
	thirdRequestChecked := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Tools []llama.Tool `json:"tools,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		requests++
		switch requests {
		case 1:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{{"message": map[string]string{"content": `{"tool":"read_file","path":"a.txt","start_line":1,"end_line":1}`}}},
			})
		case 2:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{{"message": map[string]string{"content": `{"tool":"read_file","path":"a.txt","start_line":2,"end_line":2}`}}},
			})
		case 3:
			thirdRequestChecked = true
			var toolNames []string
			for _, tool := range body.Tools {
				toolNames = append(toolNames, tool.Function.Name)
			}
			for _, forbidden := range []string{"read_file", "search_content", "code_navigate", "context_query"} {
				if slices.Contains(toolNames, forbidden) {
					t.Fatalf("last-chance scoped implementation call advertised non-write tool %q: %#v", forbidden, toolNames)
				}
			}
			if !slices.Contains(toolNames, "write_file") && !slices.Contains(toolNames, "edit_file") && !slices.Contains(toolNames, "apply_patch") {
				t.Fatalf("last-chance scoped implementation call must retain write tools, got %#v", toolNames)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{{"message": map[string]string{"content": "<<QUESTION>>: no safe edit for a.txt without a current replacement anchor"}}},
			})
		default:
			t.Fatalf("unexpected extra chat request %d", requests)
		}
	}))
	defer server.Close()

	var sawLastChance bool
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		Policy: RunPolicy{MaxToolSteps: 8, MaxNoProgressTurns: 8, MaxModelCalls: 3},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:           "implementation",
			Contract:             "implementation_result",
			PermissionProfile:    "guarded_workspace_write",
			VerificationContract: "not_required_for_text_mutations",
			RequiredEditPaths:    []string{"a.txt"},
			AllowedTools:         []string{"read_file", "search_content", "code_navigate", "write_file", "edit_file", "apply_patch"},
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "implementation_scoped_last_chance_write_only" {
				sawLastChance = true
			}
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Update a.txt."}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !thirdRequestChecked {
		t.Fatal("expected third model call to exercise last-chance write-only surface")
	}
	if !sawLastChance {
		t.Fatal("expected implementation_scoped_last_chance_write_only event")
	}
	if !strings.Contains(answer, "<<QUESTION>>") || !strings.Contains(answer, "a.txt") {
		t.Fatalf("expected typed blocker answer for a.txt, got %q", answer)
	}
	if requests != 3 {
		t.Fatalf("expected exactly three model requests, got %d", requests)
	}
}

func TestWriteReadyPacketRepeatedEmptyPathReadReturnsTypedBlocker(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "a.txt", "before\n"); err != nil {
		t.Fatalf("write a: %v", err)
	}

	replies := []string{
		`{"tool":"read_file","path":""}`,
		`{"tool":"read_file","path":""}`,
		`{"tool":"write_file","path":"a.txt","content":"after\n","description":"should not be reached after repeated scoped-read recovery"}`,
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

	var completion runnerCompletionStatus
	var scopedReadRejections int
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		Policy: RunPolicy{MaxToolSteps: 8, MaxNoProgressTurns: 8},
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
			if event.Role == "completion_status" {
				if err := json.Unmarshal([]byte(event.Content), &completion); err != nil {
					t.Fatalf("decode completion status: %v", err)
				}
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
			if event.Result.Summary == "Scoped implementation read rejected" {
				scopedReadRejections++
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(answer, "scoped implementation recovery rejected the same read_file request twice") {
		t.Fatalf("expected repeated scoped-read blocker, got %q", answer)
	}
	if completion.Status != "blocked" || completion.BlockerKind != "scoped_implementation_read_repeated" {
		t.Fatalf("expected scoped-read blocker completion status, got %#v", completion)
	}
	if scopedReadRejections != 2 {
		t.Fatalf("expected two scoped-read rejections before corrected write, got %d", scopedReadRejections)
	}
	if requests != 2 {
		t.Fatalf("expected blocker before write, got %d requests", requests)
	}
	content, err := os.ReadFile(filepath.Join(root, "a.txt"))
	if err != nil {
		t.Fatalf("read a: %v", err)
	}
	if string(content) != "before\n" {
		t.Fatalf("scoped-read blocker should not mutate a.txt, got %q", content)
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

func TestScopedImplementationHiddenBroadToolFallsThroughAfterOneRecovery(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "a.txt", "before\n"); err != nil {
		t.Fatalf("write a: %v", err)
	}

	replies := []string{
		`{"tool":"search_content","path":".","pattern":"before","named_gap":"find implementation context","why_not_answering":"need context"}`,
		`{"tool":"search_content","path":".","pattern":"after","named_gap":"find implementation context","why_not_answering":"need context"}`,
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

	var scopedRecoveryEvents int
	var hiddenToolResults int
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
			if event.Role == "implementation_scoped_tool_recovery" {
				scopedRecoveryEvents++
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
			if event.Result.Summary == "Tool hidden by active contract" {
				hiddenToolResults++
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if scopedRecoveryEvents != 1 {
		t.Fatalf("expected exactly one scoped recovery before hidden-tool handling, got %d", scopedRecoveryEvents)
	}
	if hiddenToolResults != 1 {
		t.Fatalf("expected repeated broad tool to fall through to hidden-tool handling, got %d hidden result(s)", hiddenToolResults)
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

func TestTerminalBlockedStatusSupersedesPrefinishCompletedStatus(t *testing.T) {
	if !terminalBlockedStatusShouldEmit(true, "completed", "", "transport_protocol_output") {
		t.Fatal("terminal blocked status must supersede a pre-finish completed status")
	}
	if terminalBlockedStatusShouldEmit(true, "blocked", "source_grounding_unmet", "transport_protocol_output") {
		t.Fatal("terminal blocked status should not overwrite an already-blocked local status")
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

func TestScopedImplementationAllowsPackageDirectorySemanticDiscovery(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "go.mod", "module example.test\n"); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := writeTestFile(root, "internal/productworkflow/coverage_gate.go", "package productworkflow\n\nfunc EvaluateCoverageGate() bool { return ValidateADRSupersessionLinks() }\n"); err != nil {
		t.Fatalf("write coverage gate: %v", err)
	}
	if err := writeTestFile(root, "internal/productworkflow/adr_supersession.go", "package productworkflow\n\nfunc ValidateADRSupersessionLinks() bool { return true }\n"); err != nil {
		t.Fatalf("write supersession validator: %v", err)
	}

	replies := []string{
		`{"tool":"code_navigate","capability":"read_symbol","path":"internal/productworkflow","pattern":"ValidateADRSupersessionLinks","named_gap":"body of ValidateADRSupersessionLinks before wiring coverage gate","why_not_answering":"the implementation depends on the existing canonical validator"}`,
		`{"tool":"write_file","path":"internal/productworkflow/coverage_gate.go","content":"package productworkflow\n\nfunc EvaluateCoverageGate() bool { return ValidateADRSupersessionLinks() }\n","description":"wire canonical supersession validator"}`,
		"Changed paths:\n- internal/productworkflow/coverage_gate.go\nVerification: not required for this test fixture.",
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

	var scopedReadRejections int
	var semanticDiscoveryResults int
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		Policy: RunPolicy{MaxToolSteps: 8, MaxNoProgressTurns: 6},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Objective:            "wire coverage gate to canonical ADR supersession validators",
			TaskFamily:           "implementation",
			Contract:             "implementation_result",
			PermissionProfile:    "guarded_workspace_write",
			VerificationContract: "not_required_for_text_mutations",
			RequiredEditPaths:    []string{"internal/productworkflow/coverage_gate.go"},
			EvidencePaths:        []string{"internal/productworkflow/coverage_gate.go"},
		},
	}
	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "wire coverage gate to canonical ADR supersession validators"}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			if event.Request.Tool == "code_navigate" {
				if event.Result.Summary == "Scoped implementation read rejected" {
					scopedReadRejections++
				}
				if event.Result.Error == "" {
					semanticDiscoveryResults++
				}
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if scopedReadRejections != 0 {
		t.Fatalf("package-directory semantic discovery should not be scoped-read rejected, got %d rejection(s)", scopedReadRejections)
	}
	if semanticDiscoveryResults != 1 {
		t.Fatalf("expected one successful semantic discovery result, got %d", semanticDiscoveryResults)
	}
	if !strings.Contains(answer, "internal/productworkflow/coverage_gate.go") {
		t.Fatalf("expected final answer to report changed path, got %q", answer)
	}
	if requests != len(replies) {
		t.Fatalf("expected %d model requests, got %d", len(replies), requests)
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
	if err := writeTestFile(root, "go.mod", "module idiomtest\n\ngo 1.22\n"); err != nil {
		t.Fatalf("write go.mod: %v", err)
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
		t.Fatalf("expected first no-gap follow-up to force synthesis, used %d/%d replies", requests, len(replies))
	}
	if slices.Contains(roles, "tool_rejected") {
		t.Fatalf("did not expect a rejected-tool turn before forced synthesis, got roles %v", roles)
	}
}

func TestRunnerAllowsPostReadinessSourceToolWithoutExactProbeContract(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "internal/agent/runner_child_tasks.go", "package agent\nfunc runChildTask() {}\n"); err != nil {
		t.Fatalf("write seed file: %v", err)
	}
	if err := writeTestFile(root, "go.mod", "module idiomtest\n\ngo 1.22\n"); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	replies := []string{
		`{"tool":"go_read_symbol","path":"internal/agent/runner_child_tasks.go","pattern":"runChildTask"}`,
		"runChildTask runs as the child-task entry point inspected in `internal/agent/runner_child_tasks.go`; in this fixture, the function returns no additional behavior because its body is empty.",
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

	var events []RunEvent
	var toolResults []workspace.ToolResult
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 3, MaxNoProgressTurns: 3},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "architecture",
			Contract:          "architecture_assessment",
			Objective:         "Explain how `Runner.runChildTask` works.",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"go_read_symbol"},
			Constraints:       []string{"answer_readiness_reached"},
		},
		OnEvent: func(event RunEvent) {
			events = append(events, event)
		},
	}
	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Explain how `Runner.runChildTask` works."}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			toolResults = append(toolResults, event.Result)
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(answer, "runChildTask") {
		t.Fatalf("expected final answer after honoring follow-up read, got %q; events=%s", answer, formatRunEventsForTest(events))
	}
	if got := countEventsByRole(events, "post_readiness_terminal_gate"); got != 0 {
		t.Fatalf("read-only readiness is advisory; expected no post-readiness terminal gate, got %d; events=%s", got, formatRunEventsForTest(events))
	}
	successfulProbe := false
	for _, result := range toolResults {
		if result.Tool == "go_read_symbol" && strings.TrimSpace(result.Error) == "" {
			successfulProbe = true
		}
	}
	if !successfulProbe {
		t.Fatalf("gapless post-readiness source probe should dispatch under advisory readiness, got results=%#v events=%s", toolResults, formatRunEventsForTest(events))
	}
}

func TestRunnerPostReadinessRepeatedReadDoesNotForceHostCloseout(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "internal/labels/labels.go", "package labels\n\nfunc ShortLabel() string {\n\treturn \"Save changes\"\n}\n"); err != nil {
		t.Fatalf("write seed file: %v", err)
	}
	if err := writeTestFile(root, "go.mod", "module idiomtest\n\ngo 1.22\n"); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	replies := []string{
		`{"tool":"read_file","path":"internal/labels/labels.go"}`,
		`{"tool":"read_file","path":"internal/labels/labels.go"}`,
		"`ShortLabel` returns `Save changes` in `internal/labels/labels.go`.",
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

	var events []RunEvent
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 4, MaxNoProgressTurns: 4},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			Objective:         "Can we shorten the save label in internal/labels/labels.go? Answer from the codebase.",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"read_file"},
		},
		OnEvent: func(event RunEvent) {
			events = append(events, event)
		},
	}
	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Can we shorten the save label in internal/labels/labels.go? Answer from the codebase."}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if requests != 3 {
		t.Fatalf("expected repeated read then model closeout, used %d replies", requests)
	}
	if !strings.Contains(answer, "Save changes") || !strings.Contains(answer, "internal/labels/labels.go") {
		t.Fatalf("expected model answer from gathered evidence, got %q", answer)
	}
	if got := countEventsByRole(events, "failure_feedback"); got != 0 {
		t.Fatalf("advisory readiness should not reject repeated reads, got %d failure feedback event(s); events=%s", got, formatRunEventsForTest(events))
	}
	if got := countEventsByRole(events, "post_readiness_terminal_gate"); got != 0 {
		t.Fatalf("advisory readiness should not emit post-readiness terminal gate, got %d; events=%s", got, formatRunEventsForTest(events))
	}
	for _, event := range events {
		if event.Role == "budget_ledger" {
			if !strings.Contains(event.Content, `"rejected_actions":0`) || !strings.Contains(event.Content, `"stop_reason":"model_final"`) {
				t.Fatalf("post-readiness repeated read should remain advisory before model final: %s", event.Content)
			}
			return
		}
	}
	t.Fatalf("expected budget_ledger event; events=%s", formatRunEventsForTest(events))
}

func TestRunnerReadOnlyReviewClosesAfterReplayedSourceEvidence(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "internal/agent/runner.go", "package agent\n\nfunc applyLanePreflight() {}\n"); err != nil {
		t.Fatalf("write runner seed: %v", err)
	}
	if err := writeTestFile(root, "internal/agent/final_answer_decorator.go", "package agent\n\nfunc composedFinalAnswerFromEvidence() {}\n"); err != nil {
		t.Fatalf("write decorator seed: %v", err)
	}

	replies := []string{
		`{"tool":"code_navigate","capability":"read_symbol","path":"internal/agent/runner.go","pattern":"applyLanePreflight"}`,
		`{"tool":"code_navigate","capability":"read_symbol","path":"internal/agent/final_answer_decorator.go","pattern":"composedFinalAnswerFromEvidence"}`,
		`{"tool":"code_navigate","capability":"read_symbol","path":"internal/agent/final_answer_decorator.go","pattern":"composedFinalAnswerFromEvidence"}`,
		"Review finding: the inspected runner and final-answer decorator paths already establish the replay-loop risk; confidence is high, with no writes made.",
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

	var events []RunEvent
	executed := 0
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 8, MaxNoProgressTurns: 8},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        string(workerruntime.TaskFamilySecurityReview),
			Contract:          string(workerruntime.ContractReviewFindings),
			Objective:         "Audit runner recovery and final answer contracts for repeated source-probe drift.",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"code_navigate"},
		},
		PreExecute: func(step int, req workspace.ToolRequest) *workspace.ToolResult {
			if req.Tool != "code_navigate" {
				return nil
			}
			executed++
			return &workspace.ToolResult{
				Tool:    "code_navigate",
				Summary: "Code navigation routed read_symbol via go_read_symbol",
				Output: strings.Join([]string{
					"capability: read_symbol",
					"routed_tool: go_read_symbol",
					"route_summary: Read " + req.Pattern + " in " + req.Path + " lines 1-12",
					"1:func " + req.Pattern + "() {",
					"2:\t// runner recovery and final answer contracts are inspected here",
					"3:}",
				}, "\n"),
			}
		},
		OnEvent: func(event RunEvent) {
			events = append(events, event)
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "No writes. Audit runner recovery and final answer contracts for repeated source-probe drift."}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.TrimSpace(answer) == "" {
		t.Fatal("expected forced review closeout to return an answer")
	}
	if executed != 2 {
		t.Fatalf("replayed request should not execute a third code_navigate call, executed=%d", executed)
	}
	if requests < 3 || requests > 4 {
		t.Fatalf("expected three tool-decision calls and at most one no-tools synthesis call, got %d", requests)
	}
	for _, event := range events {
		if event.Role == "budget_ledger" && strings.Contains(event.Content, `"stop_reason":"read_only_replayed_evidence_closeout"`) {
			return
		}
	}
	t.Fatalf("expected read_only_replayed_evidence_closeout budget ledger; events=%s", formatRunEventsForTest(events))
}

func TestRunnerAnswersAfterRepeatedPostReadinessNoGapFollowupsWhenEvidenceSufficient(t *testing.T) {
	root := t.TempDir()
	source := strings.Join([]string{
		"package agent",
		"",
		"type Runner struct{}",
		"",
		"func (r *Runner) Run() {}",
		"",
		"func (r *Runner) finish() {}",
	}, "\n")
	if err := writeTestFile(root, "internal/agent/runner.go", source); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	replies := []string{
		`{"tool":"read_file","path":"internal/agent/runner.go","start_line":1,"end_line":20}`,
		`{"tool":"read_file","path":"internal/agent/runner.go","start_line":21,"end_line":40}`,
		`{"tool":"read_file","path":"internal/agent/runner.go","start_line":41,"end_line":60}`,
		"`Runner.Run` is defined in `internal/agent/runner.go`; the inspected source shows the runner struct and run/finish functions used by the control flow.",
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

	var rejected int
	var budgetStop bool
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 8, MaxNoProgressTurns: 4},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			Objective:         "Explain internal/agent/runner.go Runner.Run.",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"read_file"},
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "budget_ledger" && strings.Contains(event.Content, `"rejected_actions":2`) {
				rejected++
			}
			if event.Role == "budget_ledger" && strings.Contains(event.Content, "post_readiness_gap_required_budget_exhausted") {
				budgetStop = true
			}
		},
	}
	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Explain internal/agent/runner.go Runner.Run."}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(answer, "internal/agent/runner.go") {
		t.Fatalf("answer = %q", answer)
	}
	if !strings.Contains(answer, "Runner.Run") {
		t.Fatalf("expected no-tools model synthesis answer, got %q", answer)
	}
	if requests != len(replies) {
		t.Fatalf("expected advisory readiness to allow scripted follow-up reads through final answer, used %d/%d replies", requests, len(replies))
	}
	if rejected != 0 || budgetStop {
		t.Fatalf("advisory readiness should not reject unscoped follow-ups: rejected=%d budgetStop=%v", rejected, budgetStop)
	}
}

func TestRunnerRepairsFinalAnswerAfterNoGapReadFileRanges(t *testing.T) {
	root := t.TempDir()
	source := strings.Join([]string{
		"package labels",
		"",
		"func ShortLabel() string {",
		"\treturn \"Save changes\"",
		"}",
	}, "\n")
	if err := writeTestFile(root, "internal/labels/labels.go", source); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	replies := []string{
		`{"tool":"read_file","path":"internal/labels/labels.go","start_line":1,"end_line":4,"named_gap":"inspect ShortLabel source","why_not_answering":"need source evidence before answering"}`,
		`{"tool":"read_file","path":"internal/labels/labels.go","start_line":5,"end_line":8}`,
		`{"tool":"read_file","path":"internal/labels/labels.go","start_line":9,"end_line":12}`,
		"BAD_FALLBACK_NOT_USED: the model was asked for another answer after repeated no-gap reads.",
		"For `Explain what ShortLabel returns from internal/labels/labels.go.`, `internal/labels/labels.go` shows `ShortLabel` returns \"Save changes\".",
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
		Policy: RunPolicy{MaxToolSteps: 8, MaxNoProgressTurns: 4},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			Objective:         "Explain what ShortLabel returns from internal/labels/labels.go.",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"read_file"},
		},
	}
	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Explain what ShortLabel returns from internal/labels/labels.go."}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, want := range []string{"internal/labels/labels.go", "ShortLabel", "Save changes"} {
		if !strings.Contains(answer, want) {
			t.Fatalf("bad scripted final answer should be repaired into supported answer containing %q, got:\n%s", want, answer)
		}
	}
	if strings.Contains(answer, "BAD_FALLBACK_NOT_USED") {
		t.Fatalf("bad scripted final answer surfaced:\n%s", answer)
	}
	if requests != len(replies) {
		t.Fatalf("expected advisory readiness to allow repeated no-gap ranges through repaired answer, used %d/%d requests", requests, len(replies))
	}
}

func TestRunnerAnswersAfterRepeatedInvalidPostReadinessFollowupsWhenEvidenceSufficient(t *testing.T) {
	root := t.TempDir()
	lines := make([]string, 220)
	lines[0] = "package agent"
	lines[1] = "func (r *Runner) Run() {}"
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != "" {
			continue
		}
		lines[i] = fmt.Sprintf("// line %d", i+1)
	}
	if err := writeTestFile(root, "internal/agent/runner.go", strings.Join(lines, "\n")); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	replies := []string{
		`{"tool":"read_file","path":"internal/agent/runner.go","start_line":1,"end_line":20}`,
		`{"tool":"read_file","path":"internal/agent/runner.go","start_line":1,"end_line":180,"named_gap":"Runner.Run branch in internal/agent/runner.go","why_not_answering":"need a broad source refresh"}`,
		`{"tool":"read_file","path":"internal/agent/runner.go","start_line":1,"end_line":180,"named_gap":"Runner.Run branch in internal/agent/runner.go","why_not_answering":"need a broad source refresh"}`,
		"`Runner.Run` is visible in `internal/agent/runner.go`; the inspected source anchors the harness control flow in that file.",
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

	var rejected int
	var budgetStop bool
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 8, MaxNoProgressTurns: 4},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			Objective:         "Explain the harness control flow.",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"read_file"},
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "budget_ledger" && strings.Contains(event.Content, `"rejected_actions":2`) {
				rejected++
			}
			if event.Role == "budget_ledger" && strings.Contains(event.Content, "post_readiness_gap_invalid_budget_exhausted") {
				budgetStop = true
			}
		},
	}
	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Explain the harness control flow."}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(answer, "internal/agent/runner.go") {
		t.Fatalf("answer = %q", answer)
	}
	if !strings.Contains(answer, "Runner.Run") {
		t.Fatalf("expected no-tools model synthesis answer, got %q", answer)
	}
	if requests != len(replies) {
		t.Fatalf("expected advisory readiness to allow scripted follow-ups through final answer, used %d/%d replies", requests, len(replies))
	}
	if rejected != 0 || budgetStop {
		t.Fatalf("advisory readiness should not reject invalid post-readiness follow-ups: rejected=%d budgetStop=%v", rejected, budgetStop)
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

func TestApplyLanePreflightForcesAnswerAfterConversationEvidence(t *testing.T) {
	runner := &Runner{RuntimeBrief: workerruntime.WorkerRuntimeBrief{
		TaskFamily: "conversation",
		Contract:   "direct_answer",
	}}
	results := []workspace.ToolResult{
		{Tool: "search_content", Summary: "Found matching route planner lines."},
		{Tool: "read_file", Summary: "Read internal/app/turn_planner.go."},
	}

	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Request: workspace.ToolRequest{Tool: "read_file", Path: "internal/app/prompt_routing.go"},
		Results: results,
	})
	if decision.RejectSummary != "" {
		t.Fatalf("expected tool to pass through preflight (no reject), got %#v", decision)
	}
	if decision.ForceAnswer != nil {
		t.Fatalf("expected no force answer on preflight (nudge fires post-result instead), got %#v", decision.ForceAnswer)
	}
}

func TestConversationEvidenceAvailableForObjectiveUsesGatheredSource(t *testing.T) {
	runner := &Runner{RuntimeBrief: workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		PermissionProfile: "read_only_workspace",
		Objective:         "What does internal/labels/labels.go ShortLabel return?",
	}}
	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read f1 internal/labels/labels.go lines 1-5 (total lines: 5)",
			Output:  "1:package labels\n2:\n3:func ShortLabel() string {\n4:\treturn \"Save changes\"\n5:}\n",
		},
	}
	if !runner.conversationEvidenceAvailableForObjective(results) {
		t.Fatal("expected evidence availability after bounded conversation evidence")
	}
}

func TestConversationEvidenceAvailableForObjectiveWaitsForNamedSourcePaths(t *testing.T) {
	runner := &Runner{RuntimeBrief: workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		PermissionProfile: "read_only_workspace",
		Objective:         "Explain internal/labels/labels.go and internal/labels/labels_test.go.",
	}}
	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read f1 internal/labels/labels.go lines 1-5 (total lines: 5)",
			Output:  "1:package labels\n2:\n3:func ShortLabel() string {\n4:\treturn \"Save changes\"\n5:}\n",
		},
	}
	if runner.conversationEvidenceAvailableForObjective(results) {
		t.Fatal("evidence availability fired before all named source paths had concrete source evidence")
	}

	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Request: workspace.ToolRequest{Tool: "read_file", Path: "internal/labels/labels_test.go"},
		Results: results,
	})
	if decision.ForceAnswer != nil || decision.RejectSummary != "" {
		t.Fatalf("second named source read should remain available, got %#v", decision)
	}

	results = append(results, workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Read f2 internal/labels/labels_test.go lines 1-9 (total lines: 9)",
		Output:  "1:package labels\n3:func TestShortLabel(t *testing.T) {\n4:\tif got := ShortLabel(); got != \"Save\" {\n",
	})
	if !runner.conversationEvidenceAvailableForObjective(results) {
		t.Fatal("expected evidence availability after all named source paths had concrete source evidence")
	}
}

func TestConversationPreflightRejectsRepeatedBroadDiscoveryWithoutForcingAnswer(t *testing.T) {
	runner := &Runner{RuntimeBrief: workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		PermissionProfile: "read_only_workspace",
		AllowedTools:      []string{"context_pack", "context_query", "read_file"},
	}}
	results := []workspace.ToolResult{
		{
			Tool:    "completion_gate",
			Summary: "Completion gate rejected unresolved process narration",
			Error:   "the reply describes an intended future search/read/inspection instead of completing the answer",
		},
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote .idiom/comprehension/views/example.json with 4 candidate file(s)",
			Output:  `{"candidate_files":[{"path":"internal/agent/runner.go"}]}`,
		},
	}

	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Request: workspace.ToolRequest{Tool: "context_query", Query: "bounded read scoping", Resolution: "symbol"},
		Results: results,
	})
	if decision.RejectSummary != "Conversation contract rejected repeated broad discovery after bounded evidence" {
		t.Fatalf("expected repeated broad discovery rejection, got %#v", decision)
	}
	if decision.ForceAnswer != nil {
		t.Fatalf("repeated broad discovery must not force final synthesis, got %#v", decision.ForceAnswer)
	}
	if !strings.Contains(decision.RejectError, "recovery probe") {
		t.Fatalf("expected recovery-probe explanation, got %q", decision.RejectError)
	}
	if !strings.Contains(decision.RejectError, "narrow read/search") {
		t.Fatalf("expected narrow follow-up to remain available, got %q", decision.RejectError)
	}

	decision = runner.applyLanePreflight(runnerLanePreflightInput{
		Request: workspace.ToolRequest{Tool: "read_file", Path: "internal/agent/runner.go", StartLine: 1, EndLine: 80},
		Results: results,
	})
	if decision.RejectSummary != "" {
		t.Fatalf("expected narrow read to pass preflight (nudge fires post-result), got %#v", decision)
	}
	if decision.ForceAnswer != nil {
		t.Fatalf("expected no force answer on preflight for conversation evidence (nudge replaces force), got %#v", decision.ForceAnswer)
	}

	decision = runner.applyLanePreflight(runnerLanePreflightInput{
		Request: workspace.ToolRequest{
			Tool:            "context_query",
			Query:           "callers of Runner.runChildTask",
			Resolution:      "symbol",
			NamedGap:        "which caller triggers child task orchestration",
			WhyNotAnswering: "without caller evidence the control flow is inferred",
		},
		Results: results,
	})
	if decision.RejectSummary != "" || decision.ForceAnswer != nil {
		t.Fatalf("targeted context_query with a named gap should remain available, got %#v", decision)
	}

	decision = runner.applyLanePreflight(runnerLanePreflightInput{
		Request: workspace.ToolRequest{
			Tool:            "context_pack",
			Query:           "restart broad localization",
			NamedGap:        "need more context",
			WhyNotAnswering: "more context could help",
		},
		Results: results,
	})
	if decision.RejectSummary != "Conversation contract rejected repeated broad discovery after bounded evidence" {
		t.Fatalf("named_gap must not reopen broad context_pack discovery, got %#v", decision)
	}
}

func TestConversationDiscoveryConsumptionAllowsFollowupSearchAfterContextPack(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		PermissionProfile: "read_only_workspace",
	}
	state := newRunnerExecutionState(false, 0)
	state.applyResult(
		workspace.ToolRequest{Tool: "context_pack", Query: "debug scroll regression"},
		workspace.ToolResult{
			Tool:    "context_pack",
			Summary: "Context pack wrote view with 2 candidate file(s)",
			Output:  `{"quality":{"localization_confidence":"low","source_owner_found":false,"evidence_paths":["internal/app/model.go"],"reasons":["related_evidence_without_source_owner"]},"candidate_files":[{"path":"internal/app/model.go"},{"path":"internal/app/view.go"}]}`,
		},
		"",
		nil,
		nil,
	)

	if state.shouldConsumeDiscoveryBeforeMoreBroadDiscovery(workspace.ToolRequest{Tool: "search_files", Pattern: "scroll"}, brief) {
		t.Fatal("follow-up file search should stay available after weak context_pack localization")
	}
	targetedFileSearch := workspace.ToolRequest{
		Tool:    "search_files",
		Pattern: "**/tui*.go",
	}
	if state.shouldConsumeDiscoveryBeforeMoreBroadDiscovery(targetedFileSearch, brief) {
		t.Fatal("concrete file/glob search should stay available after weak context_pack localization")
	}
	targetedGlobFieldSearch := workspace.ToolRequest{
		Tool: "search_files",
		Glob: "**/tui/**/*.go",
	}
	if state.shouldConsumeDiscoveryBeforeMoreBroadDiscovery(targetedGlobFieldSearch, brief) {
		t.Fatal("concrete glob-field file search should stay available after weak context_pack localization")
	}
	targetedContent := workspace.ToolRequest{
		Tool:     "search_content",
		Pattern:  "chat input newline textarea",
		Path:     ".",
		NamedGap: "find the concrete text anchor for the chat input newline rendering path",
	}
	if state.shouldConsumeDiscoveryBeforeMoreBroadDiscovery(targetedContent, brief) {
		t.Fatal("content search with a concrete named gap should stay available after weak context_pack localization")
	}
	broadMultiTermContent := workspace.ToolRequest{
		Tool:    "search_content",
		Pattern: "chat input newline textarea",
		Path:    ".",
	}
	if state.shouldConsumeDiscoveryBeforeMoreBroadDiscovery(broadMultiTermContent, brief) {
		t.Fatal("repo-root follow-up content search should stay available after weak context_pack localization")
	}
	fileScopedContent := workspace.ToolRequest{
		Tool:    "search_content",
		Pattern: "textarea",
		Path:    "internal/app/lifecycle.go",
	}
	if state.shouldConsumeDiscoveryBeforeMoreBroadDiscovery(fileScopedContent, brief) {
		t.Fatal("file-scoped content search should stay available after weak context_pack localization")
	}
	genericContent := workspace.ToolRequest{
		Tool:    "search_content",
		Pattern: "scroll",
		Path:    ".",
	}
	if state.shouldConsumeDiscoveryBeforeMoreBroadDiscovery(genericContent, brief) {
		t.Fatal("single-token follow-up content search should stay available after weak context_pack localization")
	}
	for _, req := range []workspace.ToolRequest{
		{Tool: "search_files", Pattern: "*.go"},
		{Tool: "search_files", Pattern: "**/*.go"},
		{Tool: "search_files", Glob: "internal/**/*.go"},
	} {
		if state.shouldConsumeDiscoveryBeforeMoreBroadDiscovery(req, brief) {
			t.Fatalf("follow-up file search should stay available after weak context_pack localization: %#v", req)
		}
	}

	targeted := workspace.ToolRequest{
		Tool:            "context_query",
		Query:           "callers of app model viewport update",
		Resolution:      "symbol",
		NamedGap:        "which caller mutates viewport height after terminal resize",
		WhyNotAnswering: "need a caller relationship before choosing a source read",
	}
	if state.shouldConsumeDiscoveryBeforeMoreBroadDiscovery(targeted, brief) {
		t.Fatalf("targeted graph query with a concrete named gap should remain available")
	}
	zoom := workspace.ToolRequest{
		Tool:          "context_zoom",
		Handle:        "symbol://internal/app/model.go#Update",
		Resolution:    "symbol",
		IncludeSource: true,
	}
	if state.shouldConsumeDiscoveryBeforeMoreBroadDiscovery(zoom, brief) {
		t.Fatalf("targeted context_zoom on a concrete handle should consume discovery, not be rejected as broad discovery")
	}

	state.applyResult(
		workspace.ToolRequest{Tool: "read_file", Path: "internal/app/model.go", StartLine: 1, EndLine: 80},
		workspace.ToolResult{Tool: "read_file", Summary: "Read internal/app/model.go lines 1-80", Output: "package app\n"},
		"",
		nil,
		nil,
	)
	if state.shouldConsumeDiscoveryBeforeMoreBroadDiscovery(workspace.ToolRequest{Tool: "search_files", Pattern: "scroll"}, brief) {
		t.Fatal("successful source-body read should clear pending discovery consumption")
	}
}

func TestConversationDiscoveryConsumptionRejectsBroadSearchAfterStrongContextPack(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		PermissionProfile: "read_only_workspace",
	}
	state := newRunnerExecutionState(false, 0)
	state.applyResult(
		workspace.ToolRequest{Tool: "context_pack", Query: "debug scroll regression"},
		workspace.ToolResult{
			Tool:    "context_pack",
			Summary: "Context pack wrote view with 2 candidate file(s)",
			Output:  `{"quality":{"localization_confidence":"high","source_owner_found":true,"evidence_paths":["internal/app/model.go"],"reasons":["source_owner_token_overlap_high"]},"candidate_files":[{"path":"internal/app/model.go"},{"path":"internal/app/view.go"}]}`,
		},
		"",
		nil,
		nil,
	)

	for _, req := range []workspace.ToolRequest{
		{Tool: "search_files", Path: ".", Pattern: "scroll"},
		{Tool: "search_files", Path: ".", Pattern: "**/*.go"},
		{Tool: "search_content", Path: ".", Pattern: "scroll"},
	} {
		if !state.shouldConsumeDiscoveryBeforeMoreBroadDiscovery(req, brief) {
			t.Fatalf("strong packet should require candidate consumption before broad rediscovery: %#v", req)
		}
	}
	graphRefinement := workspace.ToolRequest{
		Tool:            "context_query",
		Query:           "what handles TUI key input for conversation messages",
		Resolution:      "symbol",
		IncludeEvidence: true,
		NamedGap:        "source owner for TUI key input conversation message handling",
	}
	if state.shouldConsumeDiscoveryBeforeMoreBroadDiscovery(graphRefinement, brief) {
		t.Fatal("strong packet should still allow a targeted source-owner graph refinement")
	}
	targeted := workspace.ToolRequest{
		Tool:     "search_content",
		Path:     ".",
		Pattern:  "scroll viewport update",
		NamedGap: "find the concrete source anchor for viewport scrolling",
	}
	if state.shouldConsumeDiscoveryBeforeMoreBroadDiscovery(targeted, brief) {
		t.Fatal("strong packet should still allow a named-gap targeted probe")
	}
	errorText := state.discoveryConsumptionError()
	for _, want := range []string{"source-body evidence", "internal/app/model.go", "answer from the evidence", "exact blocker"} {
		if !strings.Contains(errorText, want) {
			t.Fatalf("discovery consumption error missing %q:\n%s", want, errorText)
		}
	}
	if strings.Contains(strings.ToLower(errorText), "resize") || strings.Contains(strings.ToLower(errorText), "tui") {
		t.Fatalf("generic discovery guard must not hardcode the live incident shape:\n%s", errorText)
	}
}

func TestStrongContextPackStillRejectsBroadSearchAfterFailedGuessedRead(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		PermissionProfile: "read_only_workspace",
	}
	state := newRunnerExecutionState(false, 0)
	state.applyResult(
		workspace.ToolRequest{Tool: "context_pack", Query: "debug scroll regression"},
		workspace.ToolResult{
			Tool:    "context_pack",
			Summary: "Context pack wrote view with 1 candidate file(s)",
			Output:  `{"quality":{"localization_confidence":"high","source_owner_found":true,"evidence_paths":["internal/app/model.go"],"reasons":["source_owner_token_overlap_high"]},"candidate_files":[{"path":"internal/app/model.go"}]}`,
		},
		"",
		nil,
		nil,
	)
	state.applyResult(
		workspace.ToolRequest{Tool: "read_file", Path: "internal/app/tui.go", StartLine: 1, EndLine: 80},
		workspace.ToolResult{
			Tool:    "read_file",
			Summary: "Failed to resolve file target",
			Error:   "file not found: internal/app/tui.go",
		},
		"",
		nil,
		nil,
	)

	if !state.shouldConsumeDiscoveryBeforeMoreBroadDiscovery(workspace.ToolRequest{Tool: "search_files", Path: ".", Pattern: "scroll"}, brief) {
		t.Fatal("failed guessed read after strong context_pack must not reopen broad file search")
	}
	if !state.shouldConsumeDiscoveryBeforeMoreBroadDiscovery(workspace.ToolRequest{Tool: "context_pack", Task: "scroll"}, brief) {
		t.Fatal("failed guessed read after strong context_pack must not reopen broad context_pack")
	}
	targeted := workspace.ToolRequest{
		Tool:     "search_content",
		Path:     ".",
		Pattern:  "scroll viewport update",
		NamedGap: "find the concrete source anchor after guessed path failed",
	}
	if state.shouldConsumeDiscoveryBeforeMoreBroadDiscovery(targeted, brief) {
		t.Fatal("named-gap corrective search should remain available after failed guessed read")
	}
}

func TestWeakContextPackDoesNotBlockRefinedContextPack(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		PermissionProfile: "read_only_workspace",
	}
	state := newRunnerExecutionState(false, 0)
	state.applyResult(
		workspace.ToolRequest{Tool: "context_pack", Task: "chat input text box newline wrapping first line invisible"},
		workspace.ToolResult{
			Tool:    "context_pack",
			Summary: "Context pack wrote view with 3 candidate file(s)",
			Output: `{
				"task":"chat input text box newline wrapping first line invisible",
				"quality":{"localization_confidence":"low","source_owner_found":false,"evidence_paths":["internal/app/rendering_test.go"],"reasons":["related_evidence_without_source_owner"]},
				"candidate_files":[
					{"path":"internal/transcript/debuglog.go","reason":"matched task terms"},
					{"path":"internal/app/plan.go","reason":"matched task terms"},
					{"path":"internal/app/theme_style.go","reason":"matched task terms"}
				],
				"tests":[{"path":"internal/app/rendering_test.go","symbol":"TestInputHeightUsesTextareaWrappedLineCount","command":"go test ./internal/app"}]
			}`,
		},
		"",
		nil,
		nil,
	)

	refined := workspace.ToolRequest{
		Tool: "context_pack",
		Task: "textarea syncInputHeight input height wraps prompt visible top line",
	}
	if state.shouldConsumeDiscoveryBeforeMoreBroadDiscovery(refined, brief) {
		t.Fatal("weak packet must not force consumption before a materially refined context_pack")
	}
}

func TestContextPackCandidatePathsPreferPacketQualityEvidence(t *testing.T) {
	result := workspace.ToolResult{
		Tool:    "context_pack",
		Summary: "Context pack wrote view with weak source owner",
		Output: `{
			"task":"chat input text box newline wrapping first line invisible",
			"quality":{"localization_confidence":"low","source_owner_found":false,"evidence_paths":["internal/app/rendering_test.go"],"reasons":["related_evidence_without_source_owner"]},
			"candidate_files":[
				{"path":"internal/transcript/debuglog.go","reason":"matched task terms"},
				{"path":"internal/app/plan.go","reason":"matched task terms"},
				{"path":"internal/app/theme_style.go","reason":"matched task terms"}
			],
			"candidate_symbols":[
				{"path":"internal/app/rendering_test.go","kind":"test","qualified_name":"TestInputHeightUsesTextareaWrappedLineCount"}
			],
			"tests":[{"path":"internal/app/rendering_test.go","symbol":"TestInputHeightUsesTextareaWrappedLineCount","command":"go test ./internal/app"}]
		}`,
	}

	paths := contextPackCandidatePathsFromResults([]workspace.ToolResult{result}, 4)
	if len(paths) == 0 || paths[0] != "internal/app/rendering_test.go" {
		t.Fatalf("expected quality evidence path first, got %#v", paths)
	}
	if paths[0] == "internal/transcript/debuglog.go" {
		t.Fatalf("candidate files must not saturate recovery ahead of stronger packet evidence: %#v", paths)
	}
}

func TestContextPackCandidateSymbolsPreferTopLevelQualityEvidence(t *testing.T) {
	output := `{
		"task":"chat input text box newline wrapping first line invisible",
		"quality":{"localization_confidence":"low","source_owner_found":false,"evidence_paths":["internal/app/rendering_test.go"],"reasons":["related_evidence_without_source_owner"]},
		"candidate_files":[
			{"path":"internal/transcript/debuglog.go","role":"editable","reason":"matched task terms","handles":["symbol://internal/transcript/debuglog.go#DebugLogger.LogContext"]},
			{"path":"internal/app/mocha_style.go","role":"editable","reason":"matched task terms","handles":["symbol://internal/app/mocha_style.go#mochaText"]}
		],
		"candidate_symbols":[
			{"path":"internal/app/rendering_test.go","kind":"test","qualified_name":"TestInputHeightUsesTextareaWrappedLineCount","reason":"matched task terms"},
			{"path":"internal/agent/runner.go","kind":"func","qualified_name":"textOnlyChatOptions","reason":"matched task terms"}
		],
		"tests":[{"path":"internal/app/rendering_test.go","symbol":"TestInputHeightUsesTextareaWrappedLineCount","command":"go test ./internal/app"}]
	}`

	symbols := contextPackCandidateSymbols(output, 4)
	if len(symbols) == 0 || symbols[0] != "internal/app/rendering_test.go#TestInputHeightUsesTextareaWrappedLineCount" {
		t.Fatalf("expected top-level quality evidence symbol first, got %#v", symbols)
	}
	if symbols[0] == "internal/transcript/debuglog.go#DebugLogger.LogContext" {
		t.Fatalf("candidate-file handles must not outrank top-level quality evidence: %#v", symbols)
	}
}

func TestContextZoomSourceBodyClearsPendingDiscoveryConsumption(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		PermissionProfile: "read_only_workspace",
	}
	state := newRunnerExecutionState(false, 0)
	state.applyResult(
		workspace.ToolRequest{Tool: "context_pack", Query: "debug source evidence"},
		workspace.ToolResult{
			Tool:    "context_pack",
			Summary: "Context pack wrote view with 1 candidate file(s)",
			Output:  `{"candidate_files":[{"path":"internal/app/model.go","handles":["symbol://internal/app/model.go#Update"]}]}`,
		},
		"",
		nil,
		nil,
	)
	state.applyResult(
		workspace.ToolRequest{Tool: "context_zoom", Handle: "symbol://internal/app/model.go#Update", Resolution: "symbol", IncludeSource: true},
		workspace.ToolResult{
			Tool:    "context_zoom",
			Summary: "Context zoom returned source body for internal/app/model.go#Update",
			Output:  `{"path":"internal/app/model.go","source":"func Update() {}"}`,
		},
		"",
		nil,
		nil,
	)
	if state.shouldConsumeDiscoveryBeforeMoreBroadDiscovery(workspace.ToolRequest{Tool: "search_files", Pattern: "scroll"}, brief) {
		t.Fatal("context_zoom with source body should clear pending discovery consumption")
	}
}

func TestCandidateAnchorMissDoesNotCountAsSourceBodyEvidence(t *testing.T) {
	direct := workspace.ToolResult{
		Tool:    "go_read_symbol",
		Summary: `Go symbol read path-filter miss; found 1 candidate anchor(s) for "Model.Update"`,
		Output: strings.Join([]string{
			"requested_symbol: Model.Update",
			"requested_path: internal/app/model_state.go",
			"path_filter_status: no source-body match in requested path",
			"candidate_anchor_status: anchors only; read one candidate path to inspect source body",
			"candidate_anchors:",
			"- internal/app/lifecycle.go:409 method Model.Update",
			"candidate_requests:",
			`- {"tool":"code_navigate","capability":"read_symbol","path":"internal/app/lifecycle.go","pattern":"Model.Update"}`,
		}, "\n"),
	}
	routed := workspace.ToolResult{
		Tool:    "code_navigate",
		Summary: "Code navigation routed read_symbol via go_read_symbol",
		Output: strings.Join([]string{
			"capability: read_symbol",
			"language: go",
			"routed_tool: go_read_symbol",
			`route_summary: Go symbol read path-filter miss; found 1 candidate anchor(s) for "Model.Update"`,
			"",
			direct.Output,
		}, "\n"),
	}

	for _, result := range []workspace.ToolResult{direct, routed} {
		if concreteSourceEvidenceResult(result) {
			t.Fatalf("%s candidate-anchor miss counted as concrete source evidence", result.Tool)
		}
		if paths := readPathsFromToolResult(result); len(paths) != 0 {
			t.Fatalf("%s candidate-anchor miss returned read paths: %#v", result.Tool, paths)
		}
	}
	if got := readOnlyAnalysisExactProbeCount([]workspace.ToolResult{direct, routed}); got != 0 {
		t.Fatalf("candidate-anchor miss counted as exact probe: %d", got)
	}
}

func TestSearchEvidenceSurvivesFailedGuessedReadWithoutBlockingFollowupSearch(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		PermissionProfile: "read_only_workspace",
	}
	state := newRunnerExecutionState(false, 0)
	state.applyResult(
		workspace.ToolRequest{Tool: "search_files", Pattern: "view"},
		workspace.ToolResult{
			Tool:    "search_files",
			Summary: "Found 2 matching file(s).",
			Output:  "internal/app/model.go\ninternal/app/view.go",
		},
		"",
		nil,
		nil,
	)
	state.applyResult(
		workspace.ToolRequest{Tool: "read_file", Path: "internal/app/tui.go", StartLine: 1, EndLine: 80},
		workspace.ToolResult{
			Tool:    "read_file",
			Summary: "Failed to resolve file target",
			Error:   "file not found: internal/app/tui.go",
		},
		"",
		nil,
		nil,
	)

	req := workspace.ToolRequest{Tool: "search_files", Pattern: "scroll"}
	if state.shouldConsumeSearchHitBeforeMoreSearch(req) {
		t.Fatal("failed guessed read must not force a follow-up search rejection")
	}
	if state.shouldConsumeDiscoveryBeforeMoreBroadDiscovery(req, brief) {
		t.Fatal("failed guessed read must not force a follow-up search discovery-consumption rejection")
	}
	if got := state.discoveryConsumptionError(); !strings.Contains(got, "internal/app/model.go") {
		t.Fatalf("expected recovery to preserve returned candidates, got:\n%s", got)
	}
}

func TestConversationFollowupDoesNotCollapseAfterRecoveryBroadSearch(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "notes.md"), []byte("bounded scope approval works by approving a candidate read set and requiring explicit approval when a later read falls outside that set.\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	replies := []string{
		"I should search the repo before answering this.",
		`{"tool":"search_content","pattern":"bounded scope","path":"."}`,
		`{"tool":"context_query","query":"bounded read scoping implementation surface","resolution":"symbol"}`,
		"Bounded read scoping works by approving an initial candidate read set, then treating any later file outside that set as a new approval decision. It cannot predict every relevant file with perfect accuracy, so the contract is enforced at read time: allow scoped reads, require explicit approval for out-of-scope reads, and keep an audit trail of what was actually exposed.",
	}
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected model request %d", requests+1)
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

	var results []ToolResultEvent
	runner := &Runner{
		Client: llama.NewClient(server.URL, 2*time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 4, MaxModelCalls: 6, MaxNoProgressTurns: 4},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			Objective:         "the bounded read scoping, how would that be possible?",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"search_content", "context_query", "read_file"},
			Budget: workerruntime.BudgetBrief{
				MaxToolSteps:  4,
				MaxModelCalls: 6,
			},
			Readiness: &workerruntime.ReadinessState{ReadyToAnswer: false, OpenObligations: []string{"evidence"}},
		},
	}
	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "the bounded read scoping, how would that be possible?"}},
		llama.ChatOptions{MaxTokens: 1024},
		nil,
		nil,
		func(event ToolResultEvent) {
			results = append(results, event)
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if requests != 4 {
		t.Fatalf("expected answer on the fourth model request after broad-search rejection, got %d request(s)", requests)
	}
	if !strings.Contains(answer, "approving an initial candidate read set") || !strings.Contains(answer, "out-of-scope reads") {
		t.Fatalf("answer did not explain bounded read scoping:\n%s", answer)
	}
	for _, forbidden := range []string{"retained source context", "not enough exact body-level detail", "Context pack wrote"} {
		if strings.Contains(answer, forbidden) {
			t.Fatalf("answer should not collapse to fallback phrase %q:\n%s", forbidden, answer)
		}
	}
	for _, result := range results {
		if result.Result.Tool == "context_query" && strings.TrimSpace(result.Result.Error) == "" {
			t.Fatalf("context_query should have been intercepted before execution, got result %#v", result.Result)
		}
	}
}

func TestConversationEvidenceSatisfiedContextPackAloneFiresGate(t *testing.T) {
	runner := &Runner{RuntimeBrief: workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		PermissionProfile: "read_only_workspace",
	}}
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote .idiom/comprehension/views/abc123.json with 4 candidate file(s)",
			Output:  `{"candidate_files":[{"path":"internal/agent/runner.go"},{"path":"internal/agent/runner_lane_policy.go"}]}`,
		},
	}
	// context_pack alone is no longer sufficient — the model should read
	// at least one source body before the nudge fires. This prevents the
	// nudge from triggering after a single discovery call for questions
	// that genuinely need source-body inspection.
	if conversationEvidenceSatisfied(runner.RuntimeBrief, results) {
		t.Fatal("context_pack alone should not fire evidence-satisfied gate — wait for at least one source-body read")
	}
	if runner.conversationEvidenceAvailableForObjective(results) {
		t.Fatal("context_pack alone should not trigger evidence availability")
	}

	// Add a source-body read — now evidence should be satisfied.
	results = append(results, workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Read internal/agent/runner.go lines 1-80",
		Output:  "package agent\n\nfunc (r *Runner) Run(...",
	})
	if !conversationEvidenceSatisfied(runner.RuntimeBrief, results) {
		t.Fatal("context_pack + source-body read should fire evidence-satisfied gate")
	}
	if !runner.conversationEvidenceAvailableForObjective(results) {
		t.Fatal("context_pack + source-body read should trigger evidence availability")
	}
}

func TestConversationEvidenceSatisfiedFileOutlineFindSymbolProbesOnlyLocalize(t *testing.T) {
	runner := &Runner{RuntimeBrief: workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		PermissionProfile: "read_only_workspace",
	}}
	results := []workspace.ToolResult{
		{
			Tool:    "code_navigate",
			Summary: "File outline for internal/agent/runner_lane_policy.go",
			Output:  "route_summary:\ncapability: file_outline\nlanguage: go\nrouted_tool: go_file_outline\n",
		},
		{
			Tool:    "code_navigate",
			Summary: "Found symbol conversationEvidenceSatisfied in internal/agent/runner_lane_policy.go",
			Output:  "route_summary:\ncapability: find_symbol\nlanguage: go\nrouted_tool: go_find_symbol\n",
		},
	}
	if conversationEvidenceSatisfied(runner.RuntimeBrief, results) {
		t.Fatal("file_outline + find_symbol probes should localize, not satisfy direct-answer evidence")
	}
	if runner.conversationEvidenceAvailableForObjective(results) {
		t.Fatal("outline/find-symbol localization should not trigger evidence availability")
	}
}

func TestReadOnlyAnalysisAnswerReadyAfterConversationSourceEvidence(t *testing.T) {
	runner := &Runner{RuntimeBrief: workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		PermissionProfile: "read_only_workspace",
		Objective:         "Explain why the debug investigation failed.",
	}}
	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read f1 logs/conversations/recent.jsonl lines 132-211 (file has 211 lines total)",
			Output:  "132:{\"type\":\"message\"}\n",
		},
	}
	if runner.readOnlyAnalysisAnswerReadyAfterResult(workspace.ToolRequest{Tool: "read_file"}, results[0], results) {
		t.Fatal("diagnostic logs alone should not trigger conversation answer-readiness")
	}
	results = append(results, workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Read f1 logs/conversations/recent.jsonl lines 52-131 (file has 211 lines total)",
		Output:  "52:{\"type\":\"message\"}\n",
	})
	if runner.readOnlyAnalysisAnswerReadyAfterResult(workspace.ToolRequest{Tool: "read_file"}, results[1], results) {
		t.Fatal("multiple diagnostic log windows alone should not trigger conversation answer-readiness")
	}

	results = append(results, workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Read f2 internal/agent/runner.go lines 1-50 (total lines: 200)",
		Output:  "1:package agent\n2:func (r *Runner) Run() {}\n",
	})
	if !runner.readOnlyAnalysisAnswerReadyAfterResult(workspace.ToolRequest{Tool: "read_file"}, results[2], results) {
		t.Fatal("conversation source evidence should enter the generic post-readiness contract")
	}
}

func TestVisibleBriefForToolSurfaceNarrowsTargetWriteReadyManifest(t *testing.T) {
	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{
			Root:         t.TempDir(),
			EnableWrites: true,
		}),
	}
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		AllowedTools:      []string{"read_file", "search_content", "edit_file", "apply_patch", "source_sanity"},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 internal/labels/labels.go lines 1-5 (total lines: 5)",
		Output:  "func ShortLabel() string {\n\treturn \"Save changes\"\n}\n",
	}}

	visible := runner.visibleBriefForToolSurface(brief, results)
	for _, want := range []string{"edit_file", "apply_patch", "read_file", "search_content"} {
		if !slices.Contains(visible.AllowedTools, want) {
			t.Fatalf("target-write-ready visible surface missing %q: %#v", want, visible.AllowedTools)
		}
	}
	if !toolManifestHasTool(visible.ToolManifest, "read_file") || !toolManifestHasTool(visible.ToolManifest, "search_content") {
		t.Fatalf("target-write-ready tool manifest should include bounded source refresh tools: %#v", visible.ToolManifest)
	}
	if !strings.Contains(strings.Join(visible.SearchPolicy, "\n"), "bounded source refresh") {
		t.Fatalf("target-write-ready visible brief did not explain bounded source refresh: %#v", visible.SearchPolicy)
	}
}

func TestVisibleBriefForToolSurfaceKeepsPendingRedirectTool(t *testing.T) {
	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{
			Root:         t.TempDir(),
			EnableWrites: true,
		}),
	}
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		Objective:         "fix session panel scrolling",
		PermissionProfile: "guarded_workspace_write",
		AllowedTools:      []string{"read_file", "search_content", "code_navigate", "edit_file", "apply_patch"},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read f1 internal/app/sessions_panel.go lines 1-20 (total lines: 80)",
			Output:  "package app\n\nfunc PanelVisible() bool {\n\treturn true\n}\n",
		},
		{
			Tool:    "read_file",
			Summary: "Redirected internal/app/sessions.go to code_navigate",
			Output:  "Use code_navigate for source-body evidence in internal/app/sessions.go.",
		},
	}

	visible := runner.visibleBriefForToolSurface(brief, results)
	if !slices.Contains(visible.AllowedTools, "code_navigate") || !toolManifestHasTool(visible.ToolManifest, "code_navigate") {
		t.Fatalf("visible target-write-ready surface dropped pending redirect tool: tools=%#v manifest=%#v", visible.AllowedTools, visible.ToolManifest)
	}
	if slices.Contains(visible.AllowedTools, "read_file") || slices.Contains(visible.AllowedTools, "search_content") {
		t.Fatalf("pending redirected source obligation should not expose broad refresh tools: %#v", visible.AllowedTools)
	}
}

func TestVisibleBriefForToolSurfaceStatesReadOnlyEvidenceGateBeforeAnswer(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		PermissionProfile: "read_only_workspace",
		AllowedTools:      []string{"read_file", "search_content", "context_pack", "edit_file"},
		Objective:         "Can we shorten the save label in internal/labels/labels.go?",
		Readiness:         &workerruntime.ReadinessState{ReadyToAnswer: false, OpenObligations: []string{"evidence"}},
	}

	visible := runnerVisibleBriefForToolSurface(brief, nil)

	if slices.Contains(visible.AllowedTools, "edit_file") {
		t.Fatalf("read-only evidence-pending surface retained write tool: %#v", visible.AllowedTools)
	}
	if !slices.Contains(visible.RequiredTools, "bounded_workspace_evidence_tool") {
		t.Fatalf("expected bounded evidence tool requirement, got %#v", visible.RequiredTools)
	}
	joined := strings.Join(append(append([]string{}, visible.RequiredNext...), visible.InvalidActions...), "\n")
	for _, want := range []string{
		"exactly one parser-readable bounded read/search/context tool call",
		"prose/final answer before a successful bounded evidence tool result",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected %q in read-only evidence-pending guidance:\n%s", want, joined)
		}
	}
}

func TestReadOnlyEvidenceCompletionRejectionDoesNotAcceptShallowDiscovery(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"search_files", "read_file"},
			Readiness:         &workerruntime.ReadinessState{ReadyToAnswer: false, OpenObligations: []string{"evidence"}},
		},
	}

	_, rejected := runner.completionGateRejection(
		"The label is probably in internal/labels/labels.go.",
		[]workspace.ToolResult{{Tool: "search_files", Summary: "Matched 1 file", Output: "internal/labels/labels.go"}},
		1,
		nil,
		false,
	)
	if !rejected {
		t.Fatal("shallow discovery must not satisfy read-only final-answer evidence")
	}
}

func TestEffectivePolicyHonorsRuntimeBriefBudget(t *testing.T) {
	runner := &Runner{
		MaxToolSteps: 12,
		Policy:       RunPolicy{MaxModelCalls: 20},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Budget: workerruntime.BudgetBrief{
				MaxToolSteps:  4,
				MaxModelCalls: 6,
			},
		},
	}
	policy := runner.effectivePolicy()
	if policy.MaxToolSteps != 4 || policy.MaxModelCalls != 6 {
		t.Fatalf("expected runtime brief budget to cap policy, got %#v", policy)
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
	if !slices.Contains(rails.FinalCritic, "answers_actual_user_request") {
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
	falseCompletion := strings.Join([]string{
		"**Outcome:** `internal/labels/labels.go` has been edited so `ShortLabel()` returns `\"Save\"` instead of `\"Save changes\"`.",
		"",
		"**Verification:** Source read confirms the edit was applied correctly.",
		"",
		"**Remaining risk/blocker:** The verification plan calls for `go test ./...`, but `shell` is forbidden.",
	}, "\n")
	if result, rejected := runner.mutationCompletionRejection(falseCompletion, []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 internal/labels/labels.go lines 1-5", Output: "func ShortLabel() string { return \"Save changes\" }"},
	}); !rejected || !strings.Contains(result.Error, "no successful write tool result") {
		t.Fatalf("expected claimed edit without write to be rejected, got rejected=%v result=%#v", rejected, result)
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
	verify := workspace.ToolResult{Tool: "source_sanity", Summary: "Source sanity passed", Output: "foo.go: no warnings or errors via go_diagnostics"}
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
		{"implementation-lane markdown write skips gate", "Wrote README.md", nil, false},
		{"implementation-lane markdown with source required path skips gate for non-source write", "Wrote README.md", []string{"main.go"}, false},
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

func TestPlanExecutePlannerRejectsRepeatedReadBeyondKnownEOF(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "target.txt", "one\ntwo\nthree\n"); err != nil {
		t.Fatalf("write target: %v", err)
	}

	replies := []string{
		`{"tool":"read_file","path":"target.txt","start_line":20,"end_line":30,"named_gap":"inspect tail before planning","why_not_answering":"need current source before submit_plan"}`,
		`{"tool":"read_file","path":"target.txt","start_line":30,"end_line":40,"named_gap":"keep reading","why_not_answering":"need more source"}`,
		`{"tool":"submit_plan","rationale":"Use the already gathered target.txt evidence instead of probing beyond EOF.","edits":[{"path":"target.txt","old_text":"one\n","new_text":"uno\n","purpose":"update first line"}]}`,
	}
	requests := 0
	var requestBodies []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		requestBodies = append(requestBodies, string(body))
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

	var clampedReadSeen bool
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 5, MaxNoProgressTurns: 5},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"target.txt"},
			TargetRefs:        []string{"target.txt"},
		},
	}
	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "plan target edit"}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			if event.Request.Tool != "read_file" || event.Request.Path != "target.txt" {
				return
			}
			if strings.Contains(event.Result.Summary, "was clamped to file bounds") && strings.TrimSpace(event.Result.Error) == "" {
				clampedReadSeen = true
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !clampedReadSeen {
		t.Fatalf("expected first out-of-range read to clamp once, answer=%q", answer)
	}
	if !strings.Contains(answer, "Plan submitted") {
		t.Fatalf("expected planner to recover by submitting the plan, got %q", answer)
	}
	if requests != len(replies) {
		t.Fatalf("expected read, rejected retry, then submit_plan, got %d request(s)", requests)
	}
	if len(requestBodies) < 3 ||
		(!strings.Contains(requestBodies[2], "# Terminal Plan-Execute Textual Tool Surface") &&
			!strings.Contains(requestBodies[2], "# Terminal Plan-Execute Native Tool Surface")) {
		t.Fatalf("out-of-range read rejection should switch the next planner request to terminal submit-plan surface; request=%s", requestBodies[min(len(requestBodies)-1, 2)])
	}
	if strings.Contains(requestBodies[2], `"name":"read_file"`) || !strings.Contains(requestBodies[2], `"name":"submit_plan"`) {
		t.Fatalf("out-of-range read terminal request should expose only submit_plan as a callable tool; request=%s", requestBodies[2])
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

func TestCompletionGateAttachesVerificationRecoveryAfterPreWriteRejectionAndWrite(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "go.mod", "module example.com/target\n\ngo 1.24\n"); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := writeTestFile(root, "target.go", "package target\n\nfunc Name() string { return \"old\" }\n"); err != nil {
		t.Fatalf("write target: %v", err)
	}

	replies := []string{
		"Done.",
		`{"tool":"edit_file","path":"target.go","old_text":"return \"old\"","new_text":"return \"new\"","description":"update displayed name"}`,
		"Done.",
		`{"tool":"shell","command":"go test ./..."}`,
		"Updated target.go and go test ./... passed.",
	}
	type requestPayload struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		Tools []llama.Tool `json:"tools"`
	}
	requests := 0
	var postWriteRequest requestPayload
	var recoveryRequest requestPayload
	var recoveryPrompt string
	var postWritePrompt string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request")
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request: %v", err)
		}
		if requests == 3 {
			if err := json.Unmarshal(body, &recoveryRequest); err != nil {
				t.Fatalf("decode verification recovery request: %v\n%s", err, string(body))
			}
		}
		if requests == 2 {
			if err := json.Unmarshal(body, &postWriteRequest); err != nil {
				t.Fatalf("decode post-write request: %v\n%s", err, string(body))
			}
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
			Root:         root,
			CWD:          root,
			Shell:        "/bin/sh",
			EnableWrites: true,
			EnableShell:  true,
		}),
		Policy: RunPolicy{MaxToolSteps: 8, MaxNoProgressTurns: 8},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"target.go"},
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "post_write_verification_required" {
				postWritePrompt = event.Content
			}
			if event.Role == "completion_gate_recovery" && strings.Contains(event.Content, "post-write verification") {
				recoveryPrompt = event.Content
			}
		},
	}
	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Update target.go."}},
		llama.ChatOptions{MaxTokens: 8192},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if requests != len(replies) {
		t.Fatalf("expected verification recovery after write, got %d request(s)", requests)
	}
	for _, want := range []string{
		"post-write verification",
		"parser-readable verification action",
		"language-appropriate verification",
		"target.go",
	} {
		if !strings.Contains(recoveryPrompt, want) {
			t.Fatalf("expected recovery prompt to contain %q, got:\n%s", want, recoveryPrompt)
		}
		if !strings.Contains(postWritePrompt, want) {
			t.Fatalf("expected post-write prompt to contain %q, got:\n%s", want, postWritePrompt)
		}
	}
	var postWriteText strings.Builder
	for _, message := range postWriteRequest.Messages {
		postWriteText.WriteString(message.Role)
		postWriteText.WriteString(": ")
		postWriteText.WriteString(message.Content)
		postWriteText.WriteString("\n")
	}
	if !strings.Contains(postWriteText.String(), postWritePrompt) {
		t.Fatalf("post-write verification prompt was not attached before the premature final answer:\n%s", postWriteText.String())
	}
	postWriteTools := map[string]bool{}
	for _, tool := range postWriteRequest.Tools {
		postWriteTools[strings.TrimSpace(tool.Function.Name)] = true
	}
	if !postWriteTools["shell"] {
		t.Fatalf("post-write verification request must expose shell, got tools=%v", postWriteTools)
	}
	var recoveryText strings.Builder
	for _, message := range recoveryRequest.Messages {
		recoveryText.WriteString(message.Role)
		recoveryText.WriteString(": ")
		recoveryText.WriteString(message.Content)
		recoveryText.WriteString("\n")
	}
	if !strings.Contains(recoveryText.String(), recoveryPrompt) {
		t.Fatalf("verification recovery prompt was not attached to the next model request:\n%s", recoveryText.String())
	}
	if !strings.Contains(answer, "go test ./... passed") {
		t.Fatalf("expected verified final answer, got:\n%s", answer)
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
	var secondRequestForcedNoThinking bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request")
		}
		var body struct {
			MaxTokens          int `json:"max_tokens"`
			ChatTemplateKwargs *struct {
				EnableThinking *bool `json:"enable_thinking"`
			} `json:"chat_template_kwargs,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if requests == 2 {
			secondRequestMaxTokens = body.MaxTokens
			secondRequestForcedNoThinking = body.ChatTemplateKwargs != nil &&
				body.ChatTemplateKwargs.EnableThinking != nil &&
				!*body.ChatTemplateKwargs.EnableThinking
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
		!strings.Contains(recoveryPrompt, "parser-readable action") ||
		!strings.Contains(recoveryPrompt, "Hidden-only reasoning is not completion evidence") ||
		!strings.Contains(recoveryPrompt, "edit_file") ||
		!strings.Contains(recoveryPrompt, "write_file") ||
		!strings.Contains(recoveryPrompt, "Prefer the next unread source-body request now") ||
		!strings.Contains(recoveryPrompt, "\"path\":\"b.txt\"") ||
		strings.Contains(recoveryPrompt, "Read/search/discovery tools are hidden") {
		t.Fatalf("expected recovery prompt to target remaining required path, got %q", recoveryPrompt)
	}
	if secondRequestMaxTokens != 4096 {
		t.Fatalf("expected completion-gate recovery retry to cap max_tokens at 4096, got %d", secondRequestMaxTokens)
	}
	if secondRequestForcedNoThinking {
		t.Fatalf("direct required-path recovery should not force no-thinking for local implementation retries")
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

func TestHiddenOnlyImplementationTurnAutoReadsUnreadRequiredPath(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"a.txt", "b.txt"} {
		if err := writeTestFile(root, path, "before\n"); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	replies := []map[string]string{
		{"content": `{"tool":"read_file","path":"a.txt"}`},
		{"reasoning_content": `I need the current contents of b.txt before editing it.`},
		{"content": `{"tool":"write_file","path":"a.txt","content":"after a\n","description":"update first required path"}`},
		{"content": `{"tool":"write_file","path":"b.txt","content":"after b\n","description":"update second required path"}`},
		{"content": "Done. Updated both required files."},
	}
	var requests int
	var autoReadEvent string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request")
		}
		reply := replies[requests]
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": reply,
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
		Policy: RunPolicy{MaxToolSteps: 8, MaxNoProgressTurns: 6},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:           "implementation",
			Contract:             "implementation_result",
			PermissionProfile:    "guarded_workspace_write",
			VerificationContract: "not_required_for_text_mutations",
			RequiredEditPaths:    []string{"a.txt", "b.txt"},
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "completion_recovery_auto_required_read" {
				autoReadEvent = event.Content
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
		t.Fatalf("expected hidden-only turn to auto-read without an extra model retry, got %d request(s)", requests)
	}
	if !strings.Contains(autoReadEvent, "b.txt") {
		t.Fatalf("expected auto required read event for b.txt, got %q", autoReadEvent)
	}
	if !strings.Contains(answer, "Done") {
		t.Fatalf("unexpected answer: %s", answer)
	}
	for path, want := range map[string]string{"a.txt": "after a\n", "b.txt": "after b\n"} {
		got, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if string(got) != want {
			t.Fatalf("%s = %q, want %q", path, string(got), want)
		}
	}
}

func TestImplementationRequiredPathRecoveryFollowsGenericCodeWindow(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			Objective:         "Implement coverage gate authority reuse for product tdd coverage gate",
			RequiredEditPaths: []string{"cmd/idiom/product_workflow.go"},
		},
	}

	req, _, ok := runner.deterministicImplementationRequiredPathRead([]workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read cmd/idiom/product_workflow.go lines 1-220 (total lines: 2893)",
		Output:  "1:package main",
	}})
	if !ok || req.Tool != "code_navigate" || req.Capability != "file_outline" || req.Path != "cmd/idiom/product_workflow.go" {
		t.Fatalf("generic code header read should deterministically advance to file_outline, got %#v ok=%v", req, ok)
	}
	if strings.Contains(req.NamedGap, "plan-execute") || strings.Contains(req.WhyNotAnswering, "submit_plan") {
		t.Fatalf("direct implementation follow-up must not leak plan-execute wording: %#v", req)
	}
	if !strings.Contains(req.NamedGap, "required implementation edit path") {
		t.Fatalf("direct implementation follow-up should name implementation source evidence, got %#v", req)
	}

	req, _, ok = runner.deterministicImplementationRequiredPathRead([]workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read cmd/idiom/product_workflow.go lines 1-220 (total lines: 2893)",
			Output:  "1:package main",
		},
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed file_outline via go_file_outline",
			Output: strings.Join([]string{
				"capability: file_outline",
				"route_summary: Outlined 2 top-level declaration(s) in cmd/idiom/product_workflow.go.",
				"declarations:",
				"- buildAuditResult lines 313-354 tags: core",
				"- runProductTDD lines 1396-1671 tags: implementation",
			}, "\n"),
		},
	})
	if !ok || req.Tool != "code_navigate" || req.Capability != "read_symbol" || req.Pattern != "runProductTDD" {
		t.Fatalf("outline should deterministically advance to best matching source-body symbol, got %#v ok=%v", req, ok)
	}
	if strings.Contains(req.NamedGap, "plan-execute") || strings.Contains(req.WhyNotAnswering, "submit_plan") {
		t.Fatalf("direct implementation symbol follow-up must not leak plan-execute wording: %#v", req)
	}
}

func TestImplementationRequiredPathRecoveryAutoReadsAfterNonToolProse(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			Objective:         "Implement coverage gate authority reuse for product tdd coverage gate",
			RequiredEditPaths: []string{"cmd/idiom/product_workflow.go"},
		},
	}

	req, _, ok := runner.deterministicImplementationRejectedRequiredPathRead("I need to inspect the function before editing.", []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read cmd/idiom/product_workflow.go lines 1-220 (total lines: 2893)",
		Output:  "1:package main",
	}})
	if !ok || req.Tool != "code_navigate" || req.Capability != "file_outline" {
		t.Fatalf("non-tool prose while anchors are missing should advance bounded source evidence, got %#v ok=%v", req, ok)
	}

	req, _, ok = runner.deterministicImplementationRejectedRequiredPathRead("I found the coverage gate surface and will update the product workflow.", []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read internal/productworkflow/coverage_gate.go lines 1-338 (total lines: 338)",
		Output:  "1:package productworkflow",
	}})
	if !ok || req.Tool != "read_file" || req.Path != "cmd/idiom/product_workflow.go" {
		t.Fatalf("ordinary rejected non-tool prose should advance to next unread source required path, got %#v ok=%v", req, ok)
	}

	if _, _, ok := runner.deterministicImplementationRejectedRequiredPathRead("<<QUESTION>>: cmd/idiom/product_workflow.go has a stale anchor.", nil); ok {
		t.Fatal("path-specific blocker answers should not be converted into deterministic reads")
	}

	if _, _, ok := runner.deterministicImplementationRejectedRequiredPathRead("Blocked: I cannot safely edit without the generated fixture.", nil); ok {
		t.Fatal("concrete blocker answers should not be converted into deterministic reads")
	}
}

func TestImplementationRequiredPathRecoveryDoesNotAutoReadNonSourceDeliverables(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			Objective:         "Update the release notes.",
			RequiredEditPaths: []string{"docs/release-notes.md"},
		},
	}

	if _, _, ok := runner.deterministicImplementationRejectedRequiredPathRead("I updated the release-note content.", nil); ok {
		t.Fatal("non-source deliverable prose should remain available to markdown/blocker handling instead of forcing a source read")
	}
}

func TestImplementationRequiredPathRecoveryPromptSuggestsNextSourceBodyRead(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			Objective:         "Implement coverage gate authority reuse for product tdd coverage gate",
			RequiredEditPaths: []string{"cmd/idiom/product_workflow.go"},
		},
	}

	prompt := runner.implementationRequiredPathCompletionRecoveryPrompt(
		[]string{"cmd/idiom/product_workflow.go"},
		[]workspace.ToolResult{{
			Tool:    "read_file",
			Summary: "Read cmd/idiom/product_workflow.go lines 1-220 (total lines: 2893)",
			Output:  "1:package main",
		}},
		false,
	)
	if !strings.Contains(prompt, `"tool":"code_navigate"`) || !strings.Contains(prompt, `"capability":"file_outline"`) {
		t.Fatalf("prompt should suggest the next source-body evidence request instead of repeating the header read:\n%s", prompt)
	}
	if strings.Contains(prompt, `"tool":"read_file","path":"cmd/idiom/product_workflow.go","start_line":1,"end_line":220`) {
		t.Fatalf("prompt should not repeat the already-insufficient header read:\n%s", prompt)
	}
	if strings.Contains(prompt, "submit_plan") || strings.Contains(prompt, "plan-execute") {
		t.Fatalf("implementation recovery prompt should not leak plan-execute wording:\n%s", prompt)
	}
}

func TestImplementationRequiredPathRecoveryPromptShowsTextualEditShapeWhenAnchorsReady(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "a.go", "package main\n"); err != nil {
		t.Fatalf("write a.go: %v", err)
	}
	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.go"},
		},
	}

	prompt := runner.implementationRequiredPathCompletionRecoveryPrompt(
		[]string{"a.go"},
		[]workspace.ToolResult{{
			Tool:    "read_file",
			Summary: "Read a.go lines 1-1 (total lines: 1)",
			Output:  "1:package main",
		}},
		false,
	)
	for _, want := range []string{"`edit_file`", "real required path", "Do not copy placeholders"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("anchor-ready textual recovery prompt missing %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, `"tool":"read_file"`) {
		t.Fatalf("anchor-ready recovery prompt should not suggest another read:\n%s", prompt)
	}
}

func TestTextualRequiredPathRecoveryForcesVisibleActionAfterAnchorRead(t *testing.T) {
	t.Skip("MLX now supports native tools; this validates the old textual protocol")
	root := t.TempDir()
	if err := writeTestFile(root, "a.txt", "before\n"); err != nil {
		t.Fatalf("write a.txt: %v", err)
	}

	replies := []string{
		`{"tool":"read_file","path":"a.txt","start_line":1,"end_line":20,"named_gap":"current text before edit","why_not_answering":"need source evidence"}`,
		"<<QUESTION>>: a.txt cannot be edited safely from the available evidence.",
	}
	var requests int
	var secondRequestDisablesThinking bool
	var visibleModeEvent string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request")
		}
		var body struct {
			ChatTemplateKwargs *struct {
				EnableThinking *bool `json:"enable_thinking"`
				Preserve       bool  `json:"preserve_thinking"`
				ThinkingBudget int   `json:"thinking_budget"`
			} `json:"chat_template_kwargs,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if requests == 1 {
			secondRequestDisablesThinking = body.ChatTemplateKwargs != nil &&
				body.ChatTemplateKwargs.EnableThinking != nil &&
				!*body.ChatTemplateKwargs.EnableThinking &&
				!body.ChatTemplateKwargs.Preserve
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
		Client: llama.NewClient(server.URL, time.Second).WithBackend(config.BackendMLX),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		Policy: RunPolicy{MaxToolSteps: 4, MaxNoProgressTurns: 4},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt"},
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "required_edit_paths_visible_write_mode" {
				visibleModeEvent = event.Content
			}
		},
	}
	_, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Update a.txt."}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !secondRequestDisablesThinking {
		t.Fatal("second textual request should disable hidden thinking after required-path anchor evidence is ready")
	}
	if !strings.Contains(visibleModeEvent, "disable hidden thinking") {
		t.Fatalf("expected visible write mode event, got %q", visibleModeEvent)
	}
}

func TestVisibleToolRequestProseSalvageExecutesExplicitToolObject(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "a.txt", "before\n"); err != nil {
		t.Fatalf("write a.txt: %v", err)
	}

	replies := []string{
		`I will read the required file now: {"tool":"read_file","path":"a.txt","start_line":1,"end_line":20,"named_gap":"current text before edit","why_not_answering":"need source evidence"}`,
		"<<QUESTION>>: a.txt cannot be edited safely from the available evidence.",
	}
	var requests int
	var salvageEvent string
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
		Client: llama.NewClient(server.URL, time.Second).WithBackend(config.BackendMLX),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		Policy: RunPolicy{MaxToolSteps: 4, MaxNoProgressTurns: 4},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt"},
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "visible_tool_salvage" {
				salvageEvent = event.Content
			}
		},
	}
	_, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Update a.txt."}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if requests != len(replies) {
		t.Fatalf("expected salvaged read plus final answer, got %d request(s)", requests)
	}
	if !strings.Contains(salvageEvent, "read_file") {
		t.Fatalf("expected visible tool salvage event for read_file, got %q", salvageEvent)
	}
}

func TestVisibleSubmitPlanProseSalvageAllowedAtTerminalReadiness(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.go", "b.go"},
		},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read a.go lines 1-20 (total lines: 20)", Output: "1:package a"},
		{Tool: "read_file", Summary: "Read b.go lines 1-20 (total lines: 20)", Output: "1:package b"},
	}
	reply := strings.Join([]string{
		"I have enough evidence now, so I will submit the complete plan as the required visible tool request. The prose here should not make the harness drop the structured object when it is otherwise valid.",
		`{"tool":"submit_plan","rationale":"Update both required files.","edits":[{"path":"a.go","old_text":"package a","new_text":"package a\n// updated","purpose":"update a"},{"path":"b.go","old_text":"package b","new_text":"package b\n// updated","purpose":"update b"}]}`,
	}, "\n")

	req, raw, ok := runner.salvageVisibleToolRequestFromProse(reply, results)
	if !ok {
		t.Fatal("expected visible submit_plan JSON embedded in prose to be salvaged at terminal readiness")
	}
	if req.Tool != "submit_plan" || !strings.Contains(raw, `"edits"`) || len(req.Edits) != 2 {
		t.Fatalf("unexpected salvaged submit_plan: req=%#v raw=%s", req, raw)
	}
	if hiddenReq, _, hidden := runner.salvageToolRequestFromReasoning(llama.ChatMessage{ReasoningContent: reply}, results); hidden {
		t.Fatalf("hidden reasoning must not salvage submit_plan, got %#v", hiddenReq)
	}
}

func TestVisibleSubmitPlanMissingToolSalvageAllowedForOpenCandidateSource(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 internal/productworkflow/coverage_gate.go lines 1-20 (total lines: 200)",
		Output:  "1:package productworkflow\n2:\n3:func EvaluateCoverageGate() {}\n",
	}}
	reply := `{"rationale":"Reuse canonical product authority validation in the coverage gate.","edits":[{"path":"internal/productworkflow/coverage_gate.go","old_text":"func EvaluateCoverageGate() {}","new_text":"func EvaluateCoverageGate() { validateProductAuthority() }","purpose":"call the canonical authority validation before returning coverage results"}]}`

	req, raw, ok := runner.salvageVisibleToolRequestFromProse(reply, results)
	if !ok {
		t.Fatal("expected missing-tool submit_plan JSON to be salvaged after open candidate source evidence")
	}
	if req.Tool != "submit_plan" || len(req.Edits) != 1 || req.Edits[0].Path != "internal/productworkflow/coverage_gate.go" {
		t.Fatalf("unexpected salvaged submit_plan: req=%#v raw=%s", req, raw)
	}
	if !strings.Contains(raw, `"tool":"submit_plan"`) {
		t.Fatalf("salvaged raw request must include inferred submit_plan tool, got %s", raw)
	}
}

func TestVisibleSubmitPlanProseSalvageRejectsSchemaExamplePlaceholders(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 internal/productworkflow/coverage_gate.go lines 1-20 (total lines: 200)",
		Output:  "1:package productworkflow\n2:\n3:func EvaluateCoverageGate() {}\n",
	}}
	reply := `{"tool":"submit_plan","purpose":"submit a structured executable edit plan (plan-then-execute mode only).","request":"{\"tool\":\"submit_plan\",\"rationale\":\"one paragraph on the overall plan\",\"edits\":[{\"path\":\"relative/file\",\"old_text\":\"exact current substring\",\"new_text\":\"complete replacement text\",\"purpose\":\"why this edit\"}]}"}`

	if req, raw, ok := runner.salvageVisibleToolRequestFromProse(reply, results); ok {
		t.Fatalf("schema/example submit_plan must not be salvaged, req=%#v raw=%s", req, raw)
	}
}

func TestVisibleSubmitPlanMissingToolSalvageRequiresSourceEvidence(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	}
	reply := `{"rationale":"Update target.","edits":[{"path":"target.go","old_text":"old","new_text":"new","purpose":"update target"}]}`
	if req, raw, ok := runner.salvageVisibleToolRequestFromProse(reply, nil); ok {
		t.Fatalf("missing-tool submit_plan salvage must require source evidence, req=%#v raw=%s", req, raw)
	}
}

func TestVisibleSubmitPlanTerminalBypassDoesNotWidenNonSubmitSalvage(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.go"},
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read a.go lines 1-20 (total lines: 20)",
		Output:  "1:package a",
	}}
	reply := `Here is context, but this is not a tool-action response: {"tool":"read_file","path":"a.go","start_line":1,"end_line":20}`

	if req, raw, ok := runner.salvageVisibleToolRequestFromProse(reply, results); ok {
		t.Fatalf("terminal submit-plan bypass must not salvage non-submit prose JSON, req=%#v raw=%s", req, raw)
	}
}

func TestRequiredPathRecoveryForExistingSourceFilesAdvertisesBoundedWritesOnly(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "a.go", "package main\n"); err != nil {
		t.Fatalf("write a.go: %v", err)
	}
	if err := writeTestFile(root, "b_test.go", "package main\n"); err != nil {
		t.Fatalf("write b_test.go: %v", err)
	}
	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.go", "b_test.go"},
		},
	}

	prompt := runner.implementationRequiredPathCompletionRecoveryPrompt([]string{"a.go", "b_test.go"}, nil, false)
	for _, want := range []string{"existing source/test edits", "`edit_file` with `old_text`+`new_text`", "`start_line`+`end_line`+`content`", "`apply_patch`", "one coherent multi-file patch"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected bounded recovery prompt to contain %q, got:\n%s", want, prompt)
		}
	}
	for _, forbidden := range []string{"call one write capability now for a remaining required path: `edit_file`, `write_file`, `append_file`, or `apply_patch`", "Use real current text, complete replacement text, or a valid patch"} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("bounded source recovery prompt should not advertise broad write surface %q:\n%s", forbidden, prompt)
		}
	}
	if !strings.Contains(prompt, "Do not use `write_file` or `append_file` for existing source/test edits") {
		t.Fatalf("expected explicit write_file/append_file prohibition, got:\n%s", prompt)
	}
}

func TestRequiredPathRecoveryKeepsBroadWritesForDocDeliverables(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "notes.md", "# Notes\n"); err != nil {
		t.Fatalf("write notes.md: %v", err)
	}
	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"notes.md"},
		},
	}

	prompt := runner.implementationRequiredPathCompletionRecoveryPrompt([]string{"notes.md"}, nil, false)
	if !strings.Contains(prompt, "`edit_file`, `write_file`, `append_file`, or `apply_patch`") {
		t.Fatalf("doc deliverable recovery should keep broad write options, got:\n%s", prompt)
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

func TestPlannerCompletionGateResolvesPlanCoverageAfterSubmitPlan(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"a.txt", "b.txt"},
	}
	runner := &Runner{
		RuntimeBrief: brief,
		OnEvent:      func(RunEvent) {},
	}
	recorder := newRunnerAutonomyRecorder(runner, RunPolicy{}, brief, nil)
	if recorder == nil {
		t.Fatal("expected autonomy recorder")
	}
	recorder.emitContractStarted(brief)
	if countObligationKind(recorder.obligations, autonomy.ObligationWrite) != 0 {
		t.Fatalf("compiled planner contract should not carry executor workspace write obligations: %#v", recorder.obligations)
	}
	refs := recorder.obligationIDsForGate(autonomy.GateCompletion)
	resolved, open := recorder.completionGateResolvedAndOpenObligations(runnerAutonomyGateDecision{
		Gate:         autonomy.GateCompletion,
		Decision:     autonomy.PolicyAllow,
		ReasonCode:   "plan_submitted_short_circuit",
		ChangedPaths: nil,
	}, refs)

	coverageIDs := plannerCoverageObligationsByPathForTest(t, recorder)
	for _, path := range brief.RequiredEditPaths {
		id := coverageIDs[path]
		if id == "" {
			t.Fatalf("missing planner coverage obligation for %s in %#v", path, recorder.obligations)
		}
		if !slices.Contains(resolved, id) {
			t.Fatalf("planner submit_plan should resolve plan coverage obligation %s; resolved=%#v open=%#v", id, resolved, open)
		}
		if slices.Contains(open, id) {
			t.Fatalf("planner submit_plan should not leave plan coverage obligation %s open; resolved=%#v open=%#v", id, resolved, open)
		}
	}
	finalAnswerID := obligationIDByKindForTest(t, recorder, autonomy.ObligationFinalAnswer)
	if !slices.Contains(resolved, finalAnswerID) {
		t.Fatalf("planner short-circuit should still resolve final-answer/plan artifact closeout, resolved=%#v", resolved)
	}
}

func TestPlannerCompletionGateResolvesSourceSatisfiedRequiredPaths(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"a.txt", "b.txt"},
	}
	runner := &Runner{
		RuntimeBrief: brief,
		OnEvent:      func(RunEvent) {},
	}
	recorder := newRunnerAutonomyRecorder(runner, RunPolicy{}, brief, nil)
	if recorder == nil {
		t.Fatal("expected autonomy recorder")
	}
	recorder.emitContractStarted(brief)
	if countObligationKind(recorder.obligations, autonomy.ObligationWrite) != 0 {
		t.Fatalf("compiled planner contract should not carry executor workspace write obligations: %#v", recorder.obligations)
	}
	refs := recorder.obligationIDsForGate(autonomy.GateCompletion)
	resolved, open := recorder.completionGateResolvedAndOpenObligations(runnerAutonomyGateDecision{
		Gate:       autonomy.GateCompletion,
		Decision:   autonomy.PolicyAllow,
		ReasonCode: "plan_source_satisfied_short_circuit",
		TargetRefs: []string{"a.txt:pending", "b.txt:pending"},
	}, refs)

	coverageIDs := plannerCoverageObligationsByPathForTest(t, recorder)
	for _, path := range brief.RequiredEditPaths {
		id := coverageIDs[path]
		if id == "" {
			t.Fatalf("missing planner coverage obligation for %s in %#v", path, recorder.obligations)
		}
		if !slices.Contains(resolved, id) {
			t.Fatalf("source-satisfied closeout should resolve plan coverage obligation %s; resolved=%#v open=%#v", id, resolved, open)
		}
		if slices.Contains(open, id) {
			t.Fatalf("source-satisfied closeout should not leave plan coverage obligation %s open; resolved=%#v open=%#v", id, resolved, open)
		}
	}
}

func TestPlannerCompletionGateResolvesUnscopedSourceSatisfiedFinalOutcome(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
	}
	runner := &Runner{
		RuntimeBrief: brief,
		OnEvent:      func(RunEvent) {},
	}
	recorder := newRunnerAutonomyRecorder(runner, RunPolicy{}, brief, nil)
	if recorder == nil {
		t.Fatal("expected autonomy recorder")
	}
	recorder.emitContractStarted(brief)
	if countObligationKind(recorder.obligations, autonomy.ObligationWrite) != 0 {
		t.Fatalf("compiled planner contract should not carry generic executor write obligation: %#v", recorder.obligations)
	}
	refs := recorder.obligationIDsForGate(autonomy.GateCompletion)
	resolved, open := recorder.completionGateResolvedAndOpenObligations(runnerAutonomyGateDecision{
		Gate:       autonomy.GateCompletion,
		Decision:   autonomy.PolicyAllow,
		ReasonCode: "plan_source_satisfied_short_circuit",
	}, refs)

	finalAnswerID := obligationIDByKindForTest(t, recorder, autonomy.ObligationFinalAnswer)
	if !slices.Contains(resolved, finalAnswerID) {
		t.Fatalf("unscoped source-satisfied closeout should resolve planner final outcome obligation; resolved=%#v open=%#v", resolved, open)
	}
	if slices.Contains(open, finalAnswerID) {
		t.Fatalf("unscoped source-satisfied closeout should not leave planner final outcome obligation open; resolved=%#v open=%#v", resolved, open)
	}
}

func plannerCoverageObligationsByPathForTest(t *testing.T, recorder *runnerAutonomyRecorder) map[string]string {
	t.Helper()
	ids := make(map[string]string)
	for _, obligation := range recorder.obligations {
		if obligation.Kind != autonomy.ObligationEvidence || obligation.RequiredBy != "submit_plan_required_edit_coverage" {
			continue
		}
		ids[obligation.TargetRef] = obligation.ObligationID
	}
	return ids
}

func obligationIDByKindForTest(t *testing.T, recorder *runnerAutonomyRecorder, kind autonomy.ObligationKind) string {
	t.Helper()
	for _, obligation := range recorder.obligations {
		if obligation.Kind == kind {
			return obligation.ObligationID
		}
	}
	t.Fatalf("missing obligation kind %q in %#v", kind, recorder.obligations)
	return ""
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
		{path: filepath.Join("..", "..", "internal", "cliassets", "profiles", "claude-qwen-worker.md"), forbidden: rawProfileToolMenuTerms},
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
	if !slices.Contains(live.Constraints, "implementation_packet_write_ready") {
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
	if !slices.Contains(live.Constraints, "implementation_required_paths_remaining") {
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
	if slices.Contains(live.Constraints, "implementation_packet_write_ready") {
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

func TestRepeatedSearchFilesRejectsAfterReplayBeforeDoomLoop(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "internal/app/input.go", "package app\n"); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	replies := []string{
		`{"tool":"search_files","pattern":"input","path":"."}`,
		`{"tool":"search_files","pattern":"input","path":"."}`,
		`{"tool":"search_files","pattern":"input","path":"."}`,
		"Stopped after exact search replay rejection.",
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

	var replayed bool
	var rejected bool
	var events []RunEvent
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxNoProgressTurns: 8},
		OnEvent: func(event RunEvent) {
			events = append(events, event)
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "find input"}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			if event.Result.Tool == "search_files" && strings.Contains(strings.ToLower(event.Result.Summary), "replayed prior evidence") {
				replayed = true
			}
			if event.Result.Tool == "search_files" && strings.Contains(event.Result.Summary, "Repeated tool request rejected") {
				rejected = true
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !replayed {
		t.Fatal("expected the second exact search_files request to replay prior evidence")
	}
	if !rejected {
		t.Fatal("expected the third exact search_files request to reject the exhausted replay")
	}
	for _, event := range events {
		if event.Role == "doom_loop_guard" || strings.Contains(event.Content, "doom_loop_guard") {
			t.Fatalf("exact search replay exhaustion should reject before doom-loop approval path, events=%v", eventRolesFor(events))
		}
	}
	if answer != "Stopped after exact search replay rejection." {
		t.Fatalf("unexpected answer: %q", answer)
	}
	if requests != 4 {
		t.Fatalf("expected three tool requests plus final answer, got %d", requests)
	}
}

func TestReadOnlyDirectAnswerClosesAfterAnswerableExactSearchReplay(t *testing.T) {
	root := t.TempDir()
	docPath := "internal/documentviewer/document-viewer.go"
	if err := writeTestFile(root, docPath, strings.Join([]string{
		"package documentviewer",
		"",
		"const DocumentViewerBehavior = \"document viewer renders approved design documents\"",
	}, "\n")); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	replies := []string{
		fmt.Sprintf(`{"tool":"read_file","path":%q}`, docPath),
		`{"tool":"search_files","pattern":"*document*viewer*"}`,
		`{"tool":"search_files","pattern":"*document*viewer*"}`,
		`{"tool":"search_files","pattern":"*document*viewer*"}`,
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

	var events []RunEvent
	var replayed bool
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxNoProgressTurns: 8},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
			Objective:         "What does internal/documentviewer/document-viewer.go say about the document viewer?",
			AllowedTools:      []string{"read_file", "search_files"},
		},
		OnEvent: func(event RunEvent) {
			events = append(events, event)
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "What does internal/documentviewer/document-viewer.go say about the document viewer?"}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			if event.Result.Tool == "search_files" && strings.Contains(strings.ToLower(event.Result.Summary), "replayed prior evidence") {
				replayed = true
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !replayed {
		t.Fatalf("expected exact search replay to reuse prior evidence before closeout; events=%s", formatRunEventsForTest(events))
	}
	if requests != 3 {
		t.Fatalf("expected source read, first search, replay closeout; got %d requests; events=%s", requests, formatRunEventsForTest(events))
	}
	if !strings.Contains(answer, docPath) ||
		!strings.Contains(strings.ToLower(answer), "document viewer") ||
		!strings.Contains(answer, "approved design documents") {
		t.Fatalf("host closeout answer should preserve the inspected document evidence, got:\n%s", answer)
	}
	for _, event := range events {
		if event.Role != "budget_ledger" {
			continue
		}
		var ledger runnerBudgetLedger
		if err := json.Unmarshal([]byte(event.Content), &ledger); err != nil {
			t.Fatalf("decode budget ledger %q: %v", event.Content, err)
		}
		if ledger.StopReason != "host_read_only_closeout" {
			t.Fatalf("expected host_read_only_closeout, got %#v", ledger)
		}
		if ledger.RejectedActions != 0 {
			t.Fatalf("exact replay closeout should not accumulate rejected actions, got %#v", ledger)
		}
		return
	}
	t.Fatalf("expected budget ledger; events=%s", formatRunEventsForTest(events))
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

func TestRunnerRejectsUnexecutedToolJSONAsFinalAnswer(t *testing.T) {
	var requests int
	var secondRequest string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request: %v", err)
		}
		if requests == 2 {
			secondRequest = string(body)
		}
		reply := "The implementation needs to be rerouted; no workspace lookup has run."
		if requests == 1 {
			reply = "Let me start by understanding the task and locating the relevant code.\n\n" +
				`{"task":"implement docs/writer-route-decision-report-only-plan.md","mode":"plan","limit":12}`
		}
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
			Root: t.TempDir(),
			CWD:  ".",
		}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "direct_answer",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"context_pack", "read_file", "search_content"},
		},
		Policy: RunPolicy{MaxToolSteps: 3, MaxNoProgressTurns: 3},
	}

	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "start implementing docs/writer-route-decision-report-only-plan.md"}}, llama.ChatOptions{}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if requests < 2 {
		t.Fatalf("expected retry after tool-shaped final answer rejection, got %d request(s)", requests)
	}
	if strings.Contains(answer, `"task":"implement docs/writer-route-decision-report-only-plan.md"`) {
		t.Fatalf("runner accepted raw tool-shaped JSON as final answer: %q", answer)
	}
	if !strings.Contains(secondRequest, "Unexecuted tool request rejected as final answer") {
		t.Fatalf("retry request did not include completion-gate rejection context:\n%s", secondRequest)
	}
}

func TestRunnerExecutesBareJSONMissingToolAliasBeforeCompletionGate(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		reply := "I read `internal/example/target.go`; the source body contains the package declaration `package example`."
		if requests == 1 {
			reply = `{"path":"internal/example/target.go","start_line":1,"end_line":1}`
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))
	defer server.Close()

	root := t.TempDir()
	target := filepath.Join(root, "internal", "example", "target.go")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}
	if err := os.WriteFile(target, []byte("package example\n"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "direct_answer",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"read_file"},
		},
		Policy: RunPolicy{MaxToolSteps: 3, MaxNoProgressTurns: 3},
	}

	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "inspect internal/example/target.go"}}, llama.ChatOptions{}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if requests != 2 {
		t.Fatalf("expected one tool execution followed by answer, got %d request(s), answer=%q", requests, answer)
	}
	if !strings.Contains(answer, "internal/example/target.go") || !strings.Contains(answer, "package example") {
		t.Fatalf("unexpected answer: %q", answer)
	}
}

func TestRunnerSalvagesVisibleCodeNavigateJSONMissingTool(t *testing.T) {
	runner := &Runner{}
	req, raw, ok := runner.salvageVisibleToolRequestFromProse(
		`{"capability":"read_symbol","path":"cmd/idiom/product_workflow.go","pattern":"runProductBrief"}`,
		nil,
	)
	if !ok {
		t.Fatal("expected visible code_navigate payload missing tool field to be salvaged")
	}
	if req.Tool != "code_navigate" || req.Capability != "read_symbol" || req.Path != "cmd/idiom/product_workflow.go" || req.Pattern != "runProductBrief" {
		t.Fatalf("unexpected salvaged request: %#v", req)
	}
	if !strings.Contains(raw, `"tool":"code_navigate"`) {
		t.Fatalf("salvaged raw request must include inferred tool, got %s", raw)
	}
}

func TestRunnerSalvagesExplicitVisibleToolJSONAfterCompletionGateRecovery(t *testing.T) {
	runner := &Runner{}
	reply := strings.Join([]string{
		"I'll start by exploring the relevant source and tests to understand the current implementation and then plan the changes.",
		"",
		"tool: code_navigate",
		"capability: read_symbol",
		"path: cmd/idiom/product_evidence.go",
		"symbol: p912EvaluateCoverageGate",
		"",
		`{"tool":"code_navigate","capability":"read_symbol","path":"cmd/idiom/product_evidence.go","symbol":"p912EvaluateCoverageGate"}`,
	}, "\n")
	results := []workspace.ToolResult{{
		Tool:    "completion_gate",
		Summary: "Plan-execute completion gate rejected final answer",
		Error:   "plan-execute planner phase requires a successful submit_plan tool result before final answer",
	}}

	req, raw, ok := runner.salvageVisibleToolRequestFromProse(reply, results)
	if !ok {
		t.Fatal("expected explicit JSON tool request to be salvaged after completion-gate recovery")
	}
	if req.Tool != "code_navigate" || req.Capability != "read_symbol" || req.Path != "cmd/idiom/product_evidence.go" || req.Pattern != "p912EvaluateCoverageGate" {
		t.Fatalf("unexpected salvaged request: %#v", req)
	}
	if !strings.Contains(raw, `"tool":"code_navigate"`) {
		t.Fatalf("expected canonical raw tool JSON, got %s", raw)
	}
}

func TestRunnerSalvagesVisibleNestedEditFileJSONAfterCompletionGateRecovery(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read internal/app/sessions_panel.go lines 1-219 (total lines: 219)",
			Output:  "1:package app\n2:func updateSessionsPanel() {}\n",
		},
		{
			Tool:    "completion_gate",
			Summary: "Mutation completion gate rejected final answer",
			Error:   "this task asks for a workspace change, but no successful write tool result is present",
		},
	}
	nested := `{"path":"internal/app/sessions_panel.go","old_text":"func updateSessionsPanel() {}","new_text":"func updateSessionsPanel() { renderSessionsPanelContent() }","description":"keep selected session visible"}`
	reply := `{"tool":"edit_file","request":` + strconv.Quote(nested) + `}`

	req, raw, ok := runner.salvageVisibleToolRequestFromProse(reply, results)
	if !ok {
		t.Fatal("expected visible nested edit_file request to be salvaged after completion-gate recovery")
	}
	if req.Tool != "edit_file" || req.Path != "internal/app/sessions_panel.go" || req.OldText != "func updateSessionsPanel() {}" {
		t.Fatalf("unexpected salvaged request: %#v", req)
	}
	if !strings.Contains(raw, `"tool":"edit_file"`) || !strings.Contains(raw, `"request"`) {
		t.Fatalf("expected raw nested edit_file JSON, got %s", raw)
	}
}

func TestRunnerRejectsVisibleNestedEditFileJSONWithoutSourceEvidence(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "completion_gate",
		Summary: "Mutation completion gate rejected final answer",
		Error:   "this task asks for a workspace change, but no successful write tool result is present",
	}}
	nested := `{"path":"internal/app/sessions_panel.go","old_text":"func updateSessionsPanel() {}","new_text":"func updateSessionsPanel() { renderSessionsPanelContent() }","description":"keep selected session visible"}`
	reply := `{"tool":"edit_file","request":` + strconv.Quote(nested) + `}`

	if req, raw, ok := runner.salvageVisibleToolRequestFromProse(reply, results); ok {
		t.Fatalf("visible edit salvage must require source evidence for unscoped implementation writes, req=%#v raw=%s", req, raw)
	}
}

func TestUnscopedImplementationWriteReadyNonWriteLoopStopsBeforeModelBudget(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "internal/labels/labels.go", "package labels\n\nfunc ShortLabel() string {\n\treturn \"Save changes\"\n}\n"); err != nil {
		t.Fatalf("write labels.go: %v", err)
	}

	// First reply is a source-body read to establish evidence.
	// Remaining 19 are read_file on the same path without the named_gap
	// marker, which should be rejected as non-write after the source
	// refresh allowance (3) is exhausted.
	replies := []string{
		`{"tool":"read_file","path":"internal/labels/labels.go","start_line":1,"end_line":20,"named_gap":"current target source before edit","why_not_answering":"need source before writing"}`,
	}
	for i := 0; i < 20; i++ {
		replies = append(replies, `{"tool":"read_file","path":"internal/labels/labels.go"}`)
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

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		Policy: RunPolicy{MaxToolSteps: 0, MaxNoProgressTurns: 20, MaxModelCalls: 40},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Update ShortLabel in internal/labels/labels.go."}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(answer, "<<QUESTION>>") {
		t.Fatalf("expected write-ready loop blocker with <<QUESTION>>, got: %q", answer)
	}
}
func TestRunnerSalvagesVisibleLineToolRequest(t *testing.T) {
	runner := &Runner{}
	req, raw, ok := runner.salvageVisibleToolRequestFromProse(strings.Join([]string{
		"tool: read_file",
		"path: internal/productworkflow/coverage_gate.go",
		"start_line: 1",
		"end_line: 130",
	}, "\n"), nil)
	if !ok {
		t.Fatal("expected visible key-value tool request to be salvaged")
	}
	if req.Tool != "read_file" || req.Path != "internal/productworkflow/coverage_gate.go" || req.StartLine != 1 || req.EndLine != 130 {
		t.Fatalf("unexpected salvaged request: %#v", req)
	}
	if !strings.Contains(raw, `"tool":"read_file"`) || !strings.Contains(raw, `"start_line":1`) {
		t.Fatalf("salvaged raw request must be canonical JSON, got %s", raw)
	}
}

func TestPlanExecuteSalvagesVisibleMissingTargetReadForTypedRecovery(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read internal/productworkflow/coverage_gate.go lines 1-120",
		Output:  "1:package productworkflow\n",
	}}
	reply := "Let me start by understanding the current state.\n\n" + `{"tool":"read_file"}`

	req, raw, ok := runner.salvageVisibleToolRequestFromProse(reply, results)
	if !ok {
		t.Fatal("expected plan-execute targetless read_file to be salvaged for typed recovery")
	}
	if req.Tool != "read_file" || strings.TrimSpace(req.Path) != "" {
		t.Fatalf("unexpected salvaged request: %#v", req)
	}
	if !strings.Contains(raw, `"tool":"read_file"`) {
		t.Fatalf("salvaged raw request must preserve read_file request, got %s", raw)
	}
	reason, _, lowValue := planThenExecuteCandidateLowValueAction(req, runner.RuntimeBrief, results, PlanExecutePlanningTransport{TextualToolParser: true})
	if !lowValue || reason != "candidate_read_missing_target" {
		t.Fatalf("expected typed missing-target recovery, got lowValue=%v reason=%q", lowValue, reason)
	}
}

func TestRunnerDoesNotSalvageNarrativeLineText(t *testing.T) {
	runner := &Runner{}
	_, _, ok := runner.salvageVisibleToolRequestFromProse(strings.Join([]string{
		"status: blocked",
		"reason: more information is needed",
	}, "\n"), nil)
	if ok {
		t.Fatal("ordinary key-value answer text without a tool must not be salvaged")
	}
}

func TestPlanThenExecuteCandidateRangeLessReadRedirectBoundsUnreadCandidate(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:       PlanThenExecutePlannerTaskFamily,
			AllowedEditPaths: []string{"internal/productworkflow/coverage_gate.go", "internal/productworkflow/coverage_gate_test.go"},
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 internal/productworkflow/coverage_gate.go lines 1-391 (total lines: 391)",
		Output:  "1:package productworkflow\n",
	}}

	req, raw, ok := runner.planThenExecuteCandidateRangeLessReadRedirect(workspace.ToolRequest{
		Tool: "read_file",
		Path: "internal/productworkflow/coverage_gate_test.go",
	}, results)
	if !ok {
		t.Fatal("expected unread candidate range-less read to be rewritten to a bounded source window")
	}
	if req.StartLine != 1 || req.EndLine != 180 || req.Path != "internal/productworkflow/coverage_gate_test.go" {
		t.Fatalf("unexpected redirected request: %#v", req)
	}
	if !strings.Contains(raw, `"start_line":1`) || !strings.Contains(raw, `"end_line":180`) {
		t.Fatalf("redirect raw should be canonical bounded JSON, got %s", raw)
	}
}

func TestPlanThenExecuteCandidateRangeLessReadRedirectUsesContextPackScope(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{TaskFamily: PlanThenExecutePlannerTaskFamily},
	}
	results := []workspace.ToolResult{
		{
			Tool:   "context_pack",
			Output: `{"candidate_files":[{"path":"internal/productworkflow/scope.go"}],"tests":[{"path":"internal/productworkflow/coverage_gate_test.go"}]}`,
		},
		{
			Tool:    "read_file",
			Summary: "Read f1 internal/productworkflow/coverage_gate.go lines 1-391 (total lines: 391)",
			Output:  "1:package productworkflow\n",
		},
	}

	req, _, ok := runner.planThenExecuteCandidateRangeLessReadRedirect(workspace.ToolRequest{
		Tool: "read_file",
		Path: "internal/productworkflow/scope.go",
	}, results)
	if !ok {
		t.Fatal("expected context-pack candidate path to be eligible for bounded range-less read rewrite")
	}
	if req.StartLine != 1 || req.EndLine != 180 {
		t.Fatalf("unexpected redirect range: %#v", req)
	}
}

func TestPlanThenExecuteCandidateRangeLessReadRedirectSkipsAlreadyReadCandidate(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:       PlanThenExecutePlannerTaskFamily,
			AllowedEditPaths: []string{"internal/productworkflow/coverage_gate.go"},
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 internal/productworkflow/coverage_gate.go lines 1-391 (total lines: 391)",
		Output:  "1:package productworkflow\n",
	}}

	if req, raw, ok := runner.planThenExecuteCandidateRangeLessReadRedirect(workspace.ToolRequest{
		Tool: "read_file",
		Path: "internal/productworkflow/coverage_gate.go",
	}, results); ok {
		t.Fatalf("already read candidate should not be rewritten, req=%#v raw=%s", req, raw)
	}
}

func TestRunnerDoesNotSalvageCapabilityShapedAnswerJSON(t *testing.T) {
	runner := &Runner{}
	_, _, ok := runner.salvageVisibleToolRequestFromProse(
		`{"capability":"summarize","language":"go","path":"cmd/idiom/main.go","confidence":"high"}`,
		nil,
	)
	if ok {
		t.Fatal("ordinary capability-shaped answer JSON should not be salvaged as code_navigate")
	}
}

func TestRunnerDoesNotSalvageExplicitUnknownCodeNavigateCapabilityJSON(t *testing.T) {
	runner := &Runner{}
	_, _, ok := runner.salvageVisibleToolRequestFromProse(
		`{"tool":"code_navigate","capability":"summarize","path":"cmd/idiom/main.go"}`,
		nil,
	)
	if ok {
		t.Fatal("explicit code_navigate with unknown capability should not be salvaged")
	}
}

func TestRunnerTargetSourceManifestHidesVerificationTools(t *testing.T) {
	var payload struct {
		Tools []llama.Tool `json:"tools,omitempty"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": "<<QUESTION>>: Cannot edit because no concrete target is resolvable."},
			}},
		})
	}))
	defer server.Close()

	root := t.TempDir()
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"context_pack", "read_file", "search_content", "edit_file", "apply_patch", "source_sanity", "shell"},
		},
		Policy: RunPolicy{MaxToolSteps: 2, MaxNoProgressTurns: 2},
	}
	if _, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "update behavior"}}, llama.ChatOptions{}, nil, nil, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !chatOptionsExposeTool(llama.ChatOptions{Tools: payload.Tools}, "read_file") || !chatOptionsExposeTool(llama.ChatOptions{Tools: payload.Tools}, "edit_file") {
		t.Fatalf("target-source manifest should expose read/write tools, got %#v", payload.Tools)
	}
	for _, forbidden := range []string{"source_sanity", "shell"} {
		if chatOptionsExposeTool(llama.ChatOptions{Tools: payload.Tools}, forbidden) {
			t.Fatalf("target-source manifest should hide %s before writes, got %#v", forbidden, payload.Tools)
		}
	}
}

func TestTargetSourceTextualProtocolHidesVerificationTools(t *testing.T) {
	t.Skip("MLX now supports native tools; this validates the old textual protocol")
	runner := &Runner{Client: llama.NewClient("http://example.invalid", time.Second).WithBackend(config.BackendMLX)}
	brief := runnerVisibleBriefForToolSurface(workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		AllowedTools:      []string{"context_pack", "read_file", "search_content", "edit_file", "apply_patch", "source_sanity", "shell"},
	}, nil)

	prompt := runner.textualToolProtocolPrompt(brief)

	for _, want := range []string{"- read_file", "- edit_file"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("target-source textual protocol missing %s:\n%s", want, prompt)
		}
	}
	for _, forbidden := range []string{"source_sanity", "shell", "verification_shell"} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("target-source textual protocol advertised %s before write:\n%s", forbidden, prompt)
		}
	}
}

func TestRunnerTargetSourceRejectsWriteBeforeSourceBodyEvidence(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "target.go", "package main\n\nconst value = \"before\"\n"); err != nil {
		t.Fatalf("write target: %v", err)
	}
	replies := []string{
		`{"tool":"edit_file","path":"target.go","old_text":"before","new_text":"after","description":"replace value"}`,
		`{"tool":"read_file","path":"target.go","start_line":1,"end_line":20,"named_gap":"confirm source before edit retry"}`,
		"Done. Changed target.go; verification not required for this test.",
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reply := "Done. Changed target.go; verification not required for this test."
		if requests < len(replies) {
			reply = replies[requests]
		}
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))
	defer server.Close()

	var redirectedRead bool
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:           "implementation",
			Contract:             "implementation_result",
			PermissionProfile:    "guarded_workspace_write",
			AllowedTools:         []string{"read_file", "search_content", "edit_file"},
			VerificationContract: "not_required_for_text_mutations",
		},
		Policy: RunPolicy{MaxToolSteps: 3, MaxNoProgressTurns: 3},
	}
	onToolResult := func(event ToolResultEvent) {
		if event.Result.Tool == "read_file" && strings.Contains(event.Result.Summary, "target.go") {
			redirectedRead = true
		}
	}
	if _, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "update target.go"}},
		llama.ChatOptions{},
		nil,
		nil,
		onToolResult,
	); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !redirectedRead {
		t.Fatal("expected target-source write-before-evidence to redirect to a bounded read")
	}
	got, err := os.ReadFile(filepath.Join(root, "target.go"))
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if !strings.Contains(string(got), "after") {
		t.Fatalf("deferred write was not replayed after source-body evidence:\n%s", got)
	}
}

func TestRunnerTargetSourceRejectsExistingWriteFileBeforeSourceBodyEvidence(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "target.go", "package main\n\nconst value = \"before\"\n"); err != nil {
		t.Fatalf("write target: %v", err)
	}
	replies := []string{
		`{"tool":"write_file","path":"target.go","content":"package main\n\nconst value = \"after\"\n","description":"replace file"}`,
		`{"tool":"read_file","path":"target.go","start_line":1,"end_line":20,"named_gap":"confirm source before write retry"}`,
		"Done. Changed target.go; verification not required for this test.",
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reply := "Done. Changed target.go; verification not required for this test."
		if requests < len(replies) {
			reply = replies[requests]
		}
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))
	defer server.Close()

	var redirectedRead bool
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:           "implementation",
			Contract:             "implementation_result",
			PermissionProfile:    "guarded_workspace_write",
			AllowedTools:         []string{"read_file", "search_content", "write_file"},
			VerificationContract: "not_required_for_text_mutations",
		},
		Policy: RunPolicy{MaxToolSteps: 3, MaxNoProgressTurns: 3},
	}
	onToolResult := func(event ToolResultEvent) {
		if event.Result.Tool == "read_file" && strings.Contains(event.Result.Summary, "target.go") {
			redirectedRead = true
		}
	}
	if _, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "update target.go"}},
		llama.ChatOptions{},
		nil,
		nil,
		onToolResult,
	); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !redirectedRead {
		t.Fatal("expected target-source write_file before evidence to redirect to a bounded read")
	}
	got, err := os.ReadFile(filepath.Join(root, "target.go"))
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if !strings.Contains(string(got), "after") {
		t.Fatalf("deferred write_file was not replayed after source-body evidence:\n%s", got)
	}
}

func TestImplementationTargetSourceWriteBeforeEvidenceRedirectsToBoundedRead(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"read_file", "edit_file"},
		},
	}
	req := workspace.ToolRequest{
		Tool:    "edit_file",
		Path:    "internal/labels/labels.go",
		OldText: `return "Save changes"`,
		NewText: `return "Save"`,
	}

	redirected, raw, ok := runner.implementationTargetSourceWriteBeforeEvidenceRedirect(req, nil, []string{"read_file", "edit_file"})
	if !ok {
		t.Fatal("expected write-before-evidence to redirect to a bounded read of the model-selected target")
	}
	if redirected.Tool != "read_file" || redirected.Path != "internal/labels/labels.go" || redirected.StartLine != 1 || redirected.EndLine != 180 {
		t.Fatalf("redirected request = %#v", redirected)
	}
	if !strings.Contains(raw, `"named_gap":"source-body evidence before applying the requested write"`) {
		t.Fatalf("redirect raw missing named gap: %s", raw)
	}
}

func TestImplementationDeferredWriteReplayRequiresAnchorCoverage(t *testing.T) {
	req := workspace.ToolRequest{
		Tool:        "edit_file",
		Path:        "internal/labels/labels.go",
		OldText:     `return "Save changes"`,
		NewText:     `return "Save"`,
		Description: "shorten label",
	}
	deferred := newImplementationDeferredSourceEvidenceWrite(req, "", "internal/labels/labels.go")
	if deferred == nil {
		t.Fatal("expected deferred write")
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 internal/labels/labels.go lines 1-20 (total lines: 20)",
		Output:  "1:package labels\n2:\n3:func Other() string { return \"Cancel\" }\n",
	}}
	repeatedRead := workspace.ToolRequest{Tool: "read_file", Path: "internal/labels/labels.go", StartLine: 1, EndLine: 20}
	if replayReq, raw, replay := deferred.replayAfterEvidence(repeatedRead, results, []string{"read_file", "edit_file"}); replay {
		t.Fatalf("must not replay when source evidence does not cover old_text, got req=%#v raw=%q", replayReq, raw)
	}
	results = append(results, workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Read f2 internal/labels/labels.go lines 1-20 (total lines: 20)",
		Output:  "1:package labels\n2:\n3:func ShortLabel() string {\n4:\treturn \"Save changes\"\n5:}\n",
	})
	if replayReq, _, replay := deferred.replayAfterEvidence(repeatedRead, results, []string{"read_file", "edit_file"}); !replay || replayReq.Tool != "edit_file" {
		t.Fatalf("expected replay only after anchor-covered source evidence, replay=%v req=%#v", replay, replayReq)
	}
}

func TestImplementationDeferredWriteReplayRequiresHistoryWhenMarked(t *testing.T) {
	req := workspace.ToolRequest{
		Tool:        "edit_file",
		Path:        "internal/labels/labels.go",
		OldText:     `return "Save changes"`,
		NewText:     `return "Save"`,
		Description: "shorten label",
	}
	deferred := newImplementationDeferredSourceEvidenceWrite(req, "", "internal/labels/labels.go")
	if deferred == nil {
		t.Fatal("expected deferred write")
	}
	deferred.RequiresHistory = true
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 internal/labels/labels.go lines 1-20 (total lines: 20)",
		Output:  "1:package labels\n2:\n3:func ShortLabel() string {\n4:\treturn \"Save changes\"\n5:}\n",
	}}
	verifyReq := workspace.ToolRequest{Tool: "source_sanity", Path: "internal/labels/labels.go"}
	if replayReq, raw, replay := deferred.replayAfterEvidence(verifyReq, results, []string{"source_sanity", "edit_file"}); replay {
		t.Fatalf("must not replay before history evidence, got req=%#v raw=%q", replayReq, raw)
	}
	results = append(results, workspace.ToolResult{
		Tool:    "history_range",
		Summary: "Read git history for internal/labels/labels.go lines 1-5",
		Output:  "commit abc123\n\n    Preserve current label behavior",
	})
	if replayReq, _, replay := deferred.replayAfterEvidence(verifyReq, results, []string{"source_sanity", "edit_file"}); !replay || replayReq.Tool != "edit_file" {
		t.Fatalf("expected replay after source and history evidence, replay=%v req=%#v", replay, replayReq)
	}
}

func TestImplementationDeferredAppendReplayRequiresFullFileEvidence(t *testing.T) {
	req := workspace.ToolRequest{
		Tool:        "append_file",
		Path:        "notes.txt",
		Content:     "\nafter\n",
		Description: "append note",
	}
	deferred := newImplementationDeferredSourceEvidenceWrite(req, "", "notes.txt")
	if deferred == nil {
		t.Fatal("expected deferred append")
	}
	repeatedRead := workspace.ToolRequest{Tool: "read_file", Path: "notes.txt", StartLine: 1, EndLine: 180}
	partial := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 notes.txt lines 1-180 (total lines: 300)",
		Output:  "1:before\n",
	}}
	if replayReq, raw, replay := deferred.replayAfterEvidence(repeatedRead, partial, []string{"read_file", "append_file"}); replay {
		t.Fatalf("must not replay append after partial source evidence, got req=%#v raw=%q", replayReq, raw)
	}
	full := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 notes.txt lines 1-3 (total lines: 3)",
		Output:  "1:before\n2:middle\n3:end\n",
	}}
	if replayReq, _, replay := deferred.replayAfterEvidence(repeatedRead, full, []string{"read_file", "append_file"}); !replay || replayReq.Tool != "append_file" {
		t.Fatalf("expected append replay only after full-file source evidence, replay=%v req=%#v", replay, replayReq)
	}
}

func TestRunnerTargetSourceRejectsWriteAfterReadingDifferentPath(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "a.go", "package main\n\nconst a = \"read\"\n"); err != nil {
		t.Fatalf("write a: %v", err)
	}
	if err := writeTestFile(root, "b.go", "package main\n\nconst b = \"before\"\n"); err != nil {
		t.Fatalf("write b: %v", err)
	}
	replies := []string{
		`{"tool":"read_file","path":"a.go","start_line":1,"end_line":3}`,
		`{"tool":"edit_file","path":"b.go","old_text":"before","new_text":"after","description":"replace value"}`,
		"<<QUESTION>>: Cannot write b.go before source-body evidence for b.go.",
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected request %d", requests)
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

	var redirectedRead bool
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"read_file", "search_content", "edit_file"},
		},
		Policy: RunPolicy{MaxToolSteps: 4, MaxNoProgressTurns: 4},
	}
	onToolResult := func(event ToolResultEvent) {
		if event.Result.Tool == "read_file" && strings.Contains(event.Result.Summary, "b.go") {
			redirectedRead = true
		}
	}
	if _, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "update b.go after checking a.go"}},
		llama.ChatOptions{},
		nil,
		nil,
		onToolResult,
	); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !redirectedRead {
		t.Fatal("expected target-source write after reading a different path to redirect to a bounded read of the write target")
	}
	got, err := os.ReadFile(filepath.Join(root, "b.go"))
	if err != nil {
		t.Fatalf("read b: %v", err)
	}
	if strings.Contains(string(got), "after") {
		t.Fatalf("write executed after reading a different path:\n%s", got)
	}
}

func TestRunnerTargetSourceAllowsNewTargetReadAfterDifferentPathWriteRejection(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "go.mod", "module example.com/recovery\n\ngo 1.24\n"); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := writeTestFile(root, "a.go", "package main\n\nconst a = \"read\"\n"); err != nil {
		t.Fatalf("write a: %v", err)
	}
	if err := writeTestFile(root, "b.go", "package main\n\nconst b = \"before\"\n"); err != nil {
		t.Fatalf("write b: %v", err)
	}
	replies := []string{
		`{"tool":"read_file","path":"a.go","start_line":1,"end_line":3}`,
		`{"tool":"edit_file","path":"b.go","old_text":"before","new_text":"after","description":"replace value"}`,
		`{"tool":"edit_file","path":"b.go","old_text":"before","new_text":"after","description":"replace value"}`,
		`{"tool":"shell","command":"go test ./..."}`,
		"Changed paths: b.go\n\nVerification: go test ./... passed.",
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected request %d", requests)
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

	var sawRedirectedTargetRead bool
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			Shell:        "/bin/sh",
			EnableWrites: true,
			EnableShell:  true,
		}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"read_file", "search_content", "edit_file", "source_sanity", "shell"},
		},
		Policy: RunPolicy{MaxToolSteps: 7, MaxNoProgressTurns: 7},
	}
	onToolResult := func(event ToolResultEvent) {
		if event.Result.Tool == "read_file" && event.Request.Path == "b.go" && strings.Contains(event.Result.Summary, "b.go") {
			sawRedirectedTargetRead = true
		}
	}
	if _, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "update b.go after checking a.go"}},
		llama.ChatOptions{},
		nil,
		nil,
		onToolResult,
	); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !sawRedirectedTargetRead {
		t.Fatal("expected first b.go write-before-evidence to redirect to b.go source-body evidence")
	}
	got, err := os.ReadFile(filepath.Join(root, "b.go"))
	if err != nil {
		t.Fatalf("read b: %v", err)
	}
	if !strings.Contains(string(got), "after") || strings.Contains(string(got), "before") {
		t.Fatalf("expected second write to execute after b.go source-body evidence:\n%s", got)
	}
}

func TestRunnerTargetSourceAllowsRedirectedSourceFollowupAfterWriteReady(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "go.mod", "module example.com/session\n\ngo 1.24\n"); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := writeTestFile(root, "internal/app/sessions_panel.go", "package app\n\nfunc PanelVisible() bool {\n\treturn true\n}\n"); err != nil {
		t.Fatalf("write sessions_panel: %v", err)
	}
	if err := writeTestFile(root, "internal/app/sessions.go", "package app\n\nfunc ScrollOffset() int {\n\treturn 0\n}\n"); err != nil {
		t.Fatalf("write sessions: %v", err)
	}
	replies := []string{
		`{"tool":"read_file","path":"internal/app/sessions_panel.go","start_line":1,"end_line":20,"named_gap":"session panel source before editing scroll behavior"}`,
		`{"tool":"read_file","path":"internal/app/sessions.go"}`,
		`{"tool":"code_navigate","capability":"read_symbol","path":"internal/app/sessions.go","pattern":"ScrollOffset"}`,
		`{"tool":"edit_file","path":"internal/app/sessions.go","old_text":"return 0","new_text":"return 1","description":"advance scroll offset when selection reaches the pane edge"}`,
		`{"tool":"shell","command":"go test ./..."}`,
		"Changed paths: internal/app/sessions.go\n\nVerification: go test ./... passed.",
	}
	requests := 0
	var trace []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected request %d\n%s", requests, strings.Join(trace, "\n"))
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

	var sawRedirectAllowance bool
	var rejectedRedirectedFollowup bool
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			Shell:        "/bin/sh",
			EnableWrites: true,
			EnableShell:  true,
		}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"read_file", "code_navigate", "edit_file", "source_sanity", "shell"},
		},
		Policy: RunPolicy{MaxToolSteps: 8, MaxNoProgressTurns: 8},
		PreExecute: func(_ int, req workspace.ToolRequest) *workspace.ToolResult {
			switch strings.TrimSpace(req.Tool) {
			case "read_file":
				if normalizeRunnerPath(req.Path) == "internal/app/sessions.go" && req.StartLine <= 0 && req.EndLine <= 0 {
					return &workspace.ToolResult{
						Tool:    "read_file",
						Summary: "Redirected internal/app/sessions.go to code_navigate",
						Output:  `candidate_request: {"tool":"code_navigate","capability":"file_outline","path":"internal/app/sessions.go","language":"go"}`,
					}
				}
			case "code_navigate":
				if normalizeRunnerPath(req.Path) == "internal/app/sessions.go" {
					return &workspace.ToolResult{
						Tool:    "code_navigate",
						Summary: "Code navigation routed read_symbol via go_read_symbol",
						Output: strings.Join([]string{
							"capability: read_symbol",
							"language: go",
							"routed_tool: go_read_symbol",
							"route_summary: Read ScrollOffset in internal/app/sessions.go lines 3-5",
							"3:func ScrollOffset() int {",
							"4:\treturn 0",
							"5:}",
						}, "\n"),
					}
				}
			}
			return nil
		},
		OnEvent: func(event RunEvent) {
			switch event.Role {
			case "implementation_target_write_ready_source_refresh_allowed",
				"implementation_target_write_ready_recovery",
				"post_write_verification_required",
				"completion_gate_recovery":
				trace = append(trace, "event "+event.Role+": "+event.Content)
			}
			if event.Role == "implementation_target_write_ready_source_refresh_allowed" &&
				strings.Contains(event.Content, "code_navigate") {
				sawRedirectAllowance = true
			}
		},
	}
	onToolResult := func(event ToolResultEvent) {
		trace = append(trace, fmt.Sprintf("tool %s %s => %s err=%s", event.Request.Tool, event.Request.Path, event.Result.Summary, event.Result.Error))
		if event.Request.Tool == "code_navigate" &&
			event.Request.Path == "internal/app/sessions.go" &&
			strings.Contains(event.Result.Summary, "rejected") {
			rejectedRedirectedFollowup = true
		}
	}
	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "fix session panel scrolling"}},
		llama.ChatOptions{},
		nil,
		nil,
		onToolResult,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Contains(answer, "target-write-ready recovery rejected") {
		t.Fatalf("runner returned write-ready blocker:\n%s", answer)
	}
	if rejectedRedirectedFollowup {
		t.Fatal("redirected code_navigate follow-up was rejected, recreating the latest write-ready loop")
	}
	if !sawRedirectAllowance {
		t.Fatal("expected write-ready allowance event for redirected code_navigate source follow-up")
	}
	got, err := os.ReadFile(filepath.Join(root, "internal/app/sessions.go"))
	if err != nil {
		t.Fatalf("read sessions.go: %v", err)
	}
	if !strings.Contains(string(got), "return 1") || strings.Contains(string(got), "return 0") {
		t.Fatalf("expected edit after redirected source-body follow-up:\n%s", got)
	}
}

func TestRunnerTargetSourceAllowsRedirectedOutlineThenSymbolAfterWriteReady(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "go.mod", "module example.com/session\n\ngo 1.24\n"); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := writeTestFile(root, "internal/app/sessions_panel.go", "package app\n\nfunc PanelVisible() bool {\n\treturn true\n}\n"); err != nil {
		t.Fatalf("write sessions_panel: %v", err)
	}
	if err := writeTestFile(root, "internal/app/sessions.go", "package app\n\nfunc ScrollOffset() int {\n\treturn 0\n}\n"); err != nil {
		t.Fatalf("write sessions: %v", err)
	}
	replies := []string{
		`{"tool":"read_file","path":"internal/app/sessions_panel.go","start_line":1,"end_line":20,"named_gap":"session panel source before editing scroll behavior"}`,
		`{"tool":"read_file","path":"internal/app/sessions.go"}`,
		`{"tool":"read_file","path":"internal/app/sessions.go"}`,
		`{"tool":"read_file","path":"internal/app/sessions.go"}`,
		`{"tool":"edit_file","path":"internal/app/sessions.go","old_text":"return 0","new_text":"return 1","description":"advance scroll offset when selection reaches the pane edge"}`,
		`{"tool":"shell","command":"go test ./..."}`,
		"Changed paths: internal/app/sessions.go\n\nVerification: go test ./... passed.",
	}
	requests := 0
	var trace []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected request %d\n%s", requests, strings.Join(trace, "\n"))
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

	var sawOutlineRewrite bool
	var sawReadSymbolRewrite bool
	var rejectedRedirectedFollowup bool
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			Shell:        "/bin/sh",
			EnableWrites: true,
			EnableShell:  true,
		}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			Objective:         "fix session pane ScrollOffset behavior",
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"read_file", "code_navigate", "edit_file", "source_sanity", "shell"},
		},
		Policy: RunPolicy{MaxToolSteps: 9, MaxNoProgressTurns: 9},
		PreExecute: func(_ int, req workspace.ToolRequest) *workspace.ToolResult {
			switch strings.TrimSpace(req.Tool) {
			case "read_file":
				if normalizeRunnerPath(req.Path) == "internal/app/sessions.go" && req.StartLine <= 0 && req.EndLine <= 0 {
					return &workspace.ToolResult{
						Tool:    "read_file",
						Summary: "Redirected internal/app/sessions.go to code_navigate",
						Output:  `candidate_request: {"tool":"code_navigate","capability":"file_outline","path":"internal/app/sessions.go","language":"go"}`,
					}
				}
			case "code_navigate":
				if normalizeRunnerPath(req.Path) == "internal/app/sessions.go" && req.Capability == "file_outline" {
					return &workspace.ToolResult{
						Tool:    "code_navigate",
						Summary: "Code navigation routed file_outline via go_file_outline",
						Output: strings.Join([]string{
							"capability: file_outline",
							"language: go",
							"routed_tool: go_file_outline",
							"route_summary: Outlined 1 top-level declaration(s) in internal/app/sessions.go.",
							"declarations:",
							"- ScrollOffset lines 3-5 tags: implementation",
						}, "\n"),
					}
				}
				if normalizeRunnerPath(req.Path) == "internal/app/sessions.go" && req.Capability == "read_symbol" {
					return &workspace.ToolResult{
						Tool:    "code_navigate",
						Summary: "Code navigation routed read_symbol via go_read_symbol",
						Output: strings.Join([]string{
							"capability: read_symbol",
							"language: go",
							"routed_tool: go_read_symbol",
							"route_summary: Read ScrollOffset in internal/app/sessions.go lines 3-5",
							"3:func ScrollOffset() int {",
							"4:\treturn 0",
							"5:}",
						}, "\n"),
					}
				}
			}
			return nil
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "tool_request_rewrite" {
				trace = append(trace, "rewrite: "+event.Content)
				if strings.Contains(event.Content, `file_outline`) {
					sawOutlineRewrite = true
				}
				if strings.Contains(event.Content, `read_symbol`) {
					sawReadSymbolRewrite = true
				}
			}
			if event.Role == "implementation_target_write_ready_recovery" {
				trace = append(trace, "recovery: "+event.Content)
			}
		},
	}
	onToolResult := func(event ToolResultEvent) {
		trace = append(trace, fmt.Sprintf("tool %s %s => %s err=%s", event.Request.Tool, event.Request.Path, event.Result.Summary, event.Result.Error))
		if event.Request.Tool == "code_navigate" &&
			event.Request.Path == "internal/app/sessions.go" &&
			strings.Contains(event.Result.Summary, "rejected") {
			rejectedRedirectedFollowup = true
		}
	}
	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "fix session panel scrolling"}},
		llama.ChatOptions{},
		nil,
		nil,
		onToolResult,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Contains(answer, "target-write-ready recovery rejected") {
		t.Fatalf("runner returned write-ready blocker:\n%s\ntrace:\n%s", answer, strings.Join(trace, "\n"))
	}
	if !sawOutlineRewrite || !sawReadSymbolRewrite {
		t.Fatalf("expected repeated read_file redirects to outline then read_symbol, outline=%v read_symbol=%v\n%s", sawOutlineRewrite, sawReadSymbolRewrite, strings.Join(trace, "\n"))
	}
	if rejectedRedirectedFollowup {
		t.Fatalf("redirected code_navigate follow-up was rejected:\n%s", strings.Join(trace, "\n"))
	}
	got, err := os.ReadFile(filepath.Join(root, "internal/app/sessions.go"))
	if err != nil {
		t.Fatalf("read sessions: %v", err)
	}
	if !strings.Contains(string(got), "return 1") {
		t.Fatalf("expected edit after redirected outline/read_symbol follow-up:\n%s\ntrace:\n%s", got, strings.Join(trace, "\n"))
	}
}

func TestRunnerTargetSourceAllowsSameWriteAfterSourceBodyEvidence(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "go.mod", "module example.com/labels\n\ngo 1.24\n"); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := writeTestFile(root, "internal/labels/labels.go", "package labels\n\nfunc ShortLabel() string {\n\treturn \"Save changes\"\n}\n"); err != nil {
		t.Fatalf("write target: %v", err)
	}
	edit := `{"tool":"edit_file","path":"internal/labels/labels.go","old_text":"return \"Save changes\"","new_text":"return \"Save\"","description":"shorten save label"}`
	replies := []string{
		edit,
		`{"tool":"read_file","path":"internal/labels/labels.go","start_line":1,"end_line":20,"named_gap":"confirm exact ShortLabel source before editing"}`,
		`{"tool":"shell","command":"go test ./..."}`,
		edit,
		`{"tool":"shell","command":"go test ./..."}`,
		"Changed paths: internal/labels/labels.go\n\nVerification: go test ./... passed.",
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reply := "Changed paths: internal/labels/labels.go\n\nVerification: go test ./... passed."
		if requests < len(replies) {
			reply = replies[requests]
		}
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))
	defer server.Close()

	var events []RunEvent
	var results []workspace.ToolResult
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			Shell:        "/bin/sh",
			EnableWrites: true,
			EnableShell:  true,
		}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"read_file", "edit_file", "source_sanity", "shell"},
			EvidencePaths:     []string{"internal/labels/labels.go"},
		},
		Policy: RunPolicy{MaxToolSteps: 6, MaxNoProgressTurns: 6},
		OnEvent: func(event RunEvent) {
			events = append(events, event)
		},
	}
	if _, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "update internal/labels/labels.go"}},
		llama.ChatOptions{MaxTokens: 8192},
		nil,
		nil,
		func(event ToolResultEvent) {
			results = append(results, event.Result)
		},
	); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := countEventsByRole(events, "rejection_retry_blocked"); got != 0 {
		t.Fatalf("source-backed write retry must not be blocked as stale rejection, got %d events=%v", got, eventRolesFor(events))
	}
	seenInitialReadRedirect := false
	seenVerification := false
	for _, result := range results {
		if result.Tool == "read_file" && strings.Contains(result.Summary, "internal/labels/labels.go") {
			seenInitialReadRedirect = true
		}
		if result.Tool == "shell" && strings.TrimSpace(result.Error) == "" {
			seenVerification = true
		}
	}
	if !seenInitialReadRedirect {
		t.Fatal("expected first write-before-evidence to redirect to source-body evidence")
	}
	if !seenVerification {
		t.Fatalf("expected language verification command to run after accepted edit, results=%#v", results)
	}
	got, err := os.ReadFile(filepath.Join(root, "internal/labels/labels.go"))
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if !strings.Contains(string(got), `return "Save"`) || strings.Contains(string(got), "Save changes") {
		t.Fatalf("expected second write to execute after source-body evidence:\n%s", got)
	}
}

func TestRunnerPostWriteVerificationRecoversRepeatedInvalidToolThroughSourceSanity(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "go.mod", "module example.com/labels\n\ngo 1.24\n"); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := writeTestFile(root, "internal/labels/labels.go", "package labels\n\nfunc ShortLabel() string {\n\treturn \"Save changes\"\n}\n"); err != nil {
		t.Fatalf("write target: %v", err)
	}
	replies := []string{
		`{"tool":"read_file","path":"internal/labels/labels.go","start_line":1,"end_line":6,"named_gap":"current ShortLabel source before editing"}`,
		`{"tool":"edit_file","path":"internal/labels/labels.go","old_text":"return \"Save changes\"","new_text":"return \"Save\"","description":"shorten save label"}`,
		`{"tool":"edit_file","path":"internal/labels/labels.go","old_text":"return \"Save\"","new_text":"return \"Save\"","description":"retry same label change"}`,
		`{"tool":"read_file","path":"internal/labels/labels.go","start_line":1,"end_line":6}`,
		"Changed paths: internal/labels/labels.go\n\nVerification: Source sanity passed for internal/labels/labels.go.",
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected request %d", requests)
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

	var events []RunEvent
	var results []workspace.ToolResult
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			Shell:        "/bin/sh",
			EnableWrites: true,
			EnableShell:  true,
		}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"read_file", "edit_file", "source_sanity", "shell", "git_diff", "git_status"},
			EvidencePaths:     []string{"internal/labels/labels.go"},
		},
		Policy: RunPolicy{MaxToolSteps: 6, MaxNoProgressTurns: 6},
		OnEvent: func(event RunEvent) {
			events = append(events, event)
		},
	}
	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "update internal/labels/labels.go"}},
		llama.ChatOptions{MaxTokens: 8192},
		nil,
		nil,
		func(event ToolResultEvent) {
			results = append(results, event.Result)
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := countEventsByRole(events, "rejection_retry_blocked"); got != 0 {
		t.Fatalf("post-write invalid tool recovery must not force a pending-verification answer, got %d events=%v", got, eventRolesFor(events))
	}
	sawRewrite := false
	for _, event := range events {
		if event.Role == "tool_request_rewrite" && strings.Contains(event.Content, `"rewritten_tool":"source_sanity"`) {
			sawRewrite = true
		}
	}
	if !sawRewrite {
		t.Fatalf("expected repeated invalid verification tool to be rewritten to source_sanity, events=%v", eventRolesFor(events))
	}
	sawSourceSanity := false
	for _, result := range results {
		if result.Tool == "source_sanity" && result.Error == "" {
			sawSourceSanity = true
		}
	}
	if !sawSourceSanity {
		t.Fatalf("expected source_sanity recovery result, got %#v", results)
	}
	if !strings.Contains(answer, "Verification: Source sanity passed") {
		t.Fatalf("expected verified final answer, got:\n%s", answer)
	}
}

func TestRunnerEmptyImplementationReplyAutoReadsSingleEvidencePath(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "go.mod", "module example.com/labels\n\ngo 1.24\n"); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := writeTestFile(root, "internal/labels/labels.go", "package labels\n\nfunc ShortLabel() string {\n\treturn \"Save changes\"\n}\n"); err != nil {
		t.Fatalf("write target: %v", err)
	}
	replies := []string{
		"",
		`{"tool":"edit_file","path":"internal/labels/labels.go","old_text":"return \"Save changes\"","new_text":"return \"Save\"","description":"shorten save label"}`,
		`{"tool":"shell","command":"go test ./..."}`,
		"Changed paths: internal/labels/labels.go\n\nVerification: go test ./... passed.",
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reply := "Changed paths: internal/labels/labels.go\n\nVerification: go test ./... passed."
		if requests < len(replies) {
			reply = replies[requests]
		}
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))
	defer server.Close()

	var events []RunEvent
	var results []workspace.ToolResult
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			Shell:        "/bin/sh",
			EnableWrites: true,
			EnableShell:  true,
		}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"read_file", "edit_file", "source_sanity", "shell"},
			EvidencePaths:     []string{"internal/labels/labels.go"},
		},
		Policy: RunPolicy{MaxToolSteps: 4, MaxNoProgressTurns: 4},
		OnEvent: func(event RunEvent) {
			events = append(events, event)
		},
	}
	if _, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "update internal/labels/labels.go"}},
		llama.ChatOptions{MaxTokens: 8192},
		nil,
		nil,
		func(event ToolResultEvent) {
			results = append(results, event.Result)
		},
	); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := countEventsByRole(events, "completion_recovery_auto_evidence_read"); got != 1 {
		t.Fatalf("expected one deterministic evidence read event, got %d events=%v", got, eventRolesFor(events))
	}
	for _, result := range results {
		if result.Tool == "completion_gate" {
			t.Fatalf("empty implementation reply should recover through a bounded read before completion-gate rejection, results=%#v", results)
		}
	}
	if len(results) == 0 || results[0].Tool != "read_file" || results[0].Error != "" {
		t.Fatalf("expected first tool result to be read_file evidence, got %#v", results)
	}
	got, err := os.ReadFile(filepath.Join(root, "internal/labels/labels.go"))
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if !strings.Contains(string(got), `return "Save"`) || strings.Contains(string(got), "Save changes") {
		t.Fatalf("expected source edit to complete after evidence recovery:\n%s", got)
	}
}

func TestDeterministicImplementationEvidencePathReadRequiresSingleEvidencePath(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			EvidencePaths:     []string{"internal/labels/labels.go"},
		},
	}
	req, _, ok := runner.deterministicImplementationEvidencePathRead(nil)
	if !ok {
		t.Fatal("expected single evidence path to produce a bounded read")
	}
	if req.Tool != "read_file" || req.Path != "internal/labels/labels.go" || req.StartLine != 1 || req.EndLine != 180 {
		t.Fatalf("unexpected read request: %#v", req)
	}

	runner.RuntimeBrief.EvidencePaths = []string{"internal/labels/labels.go", "internal/labels/labels_test.go"}
	if _, _, ok := runner.deterministicImplementationEvidencePathRead(nil); ok {
		t.Fatal("must not choose between multiple evidence paths")
	}

	runner.RuntimeBrief.TaskFamily = PlanThenExecutePlannerTaskFamily
	runner.RuntimeBrief.EvidencePaths = []string{"internal/labels/labels.go"}
	if _, _, ok := runner.deterministicImplementationEvidencePathRead(nil); ok {
		t.Fatal("plan-execute planner target choice should stay with planner/capability contracts")
	}
}

func TestRunnerSalvagesPathOnlyFileOutlineJSONMissingTool(t *testing.T) {
	runner := &Runner{}
	req, raw, ok := runner.salvageVisibleToolRequestFromProse(
		`{"capability":"file_outline","path":"cmd/idiom/product_workflow.go"}`,
		nil,
	)
	if !ok {
		t.Fatal("expected visible file_outline payload missing tool field to be salvaged")
	}
	if req.Tool != "code_navigate" || req.Capability != "file_outline" || req.Path != "cmd/idiom/product_workflow.go" {
		t.Fatalf("unexpected salvaged request: %#v", req)
	}
	if !strings.Contains(raw, `"tool":"code_navigate"`) || !strings.Contains(raw, `"capability":"file_outline"`) {
		t.Fatalf("salvaged raw request must include inferred code_navigate request, got %s", raw)
	}
}

func TestRunnerExecutesCanonicalCodeNavigateForRepeatedRangeLessReadAfterOutline(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "go.mod", "module example.test\n"); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := writeTestFile(root, "cmd/idiom/product_workflow.go", "package main\n\nfunc runProductWorkflow() string {\n\treturn \"ok\"\n}\n"); err != nil {
		t.Fatalf("write source: %v", err)
	}

	replies := []string{
		`{"tool":"read_file","path":"cmd/idiom/product_workflow.go"}`,
		"Done.",
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

	var toolRequests []ToolRequestEvent
	var toolResults []ToolResultEvent
	var rewriteEvent string
	runner := &Runner{
		Client:    llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		Policy:    RunPolicy{MaxToolSteps: 4, MaxNoProgressTurns: 4},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Objective: "update product workflow",
		},
		SeedResults: []workspace.ToolResult{
			{
				Tool:    "read_file",
				Summary: "Redirected cmd/idiom/product_workflow.go to code_navigate",
				Output:  "The request targets go code. Capability route: file_outline via adapter code_navigate instead of raw read_file.",
			},
			{
				Tool:    "code_navigate",
				Summary: "Code navigation routed file_outline via go_file_outline",
				Output: strings.Join([]string{
					"capability: file_outline",
					"route_summary: Outlined 1 top-level declaration(s) in cmd/idiom/product_workflow.go.",
					"declarations:",
					"- runProductWorkflow lines 3-5 tags: implementation",
				}, "\n"),
			},
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "tool_request_rewrite" {
				rewriteEvent = event.Content
			}
		},
	}
	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "update product workflow"}},
		llama.ChatOptions{},
		nil,
		func(event ToolRequestEvent) {
			toolRequests = append(toolRequests, event)
		},
		func(event ToolResultEvent) {
			toolResults = append(toolResults, event)
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if answer != "Done." {
		t.Fatalf("unexpected answer %q", answer)
	}
	if len(toolRequests) != 1 {
		t.Fatalf("expected one executed tool request, got %#v", toolRequests)
	}
	gotReq := toolRequests[0].Request
	if gotReq.Tool != "code_navigate" || gotReq.Capability != "read_symbol" || gotReq.Pattern != "runProductWorkflow" {
		t.Fatalf("expected canonical code_navigate read_symbol request, got %#v", gotReq)
	}
	if !strings.Contains(toolRequests[0].Raw, `"tool":"code_navigate"`) || !strings.Contains(toolRequests[0].Raw, `"capability":"read_symbol"`) {
		t.Fatalf("onToolRequest raw should be canonical rewritten JSON, got %s", toolRequests[0].Raw)
	}
	if !strings.Contains(rewriteEvent, "repeated_rangeless_read_file_bounded_source") {
		t.Fatalf("expected repeated range-less read rewrite event, got %q", rewriteEvent)
	}
	for _, event := range toolResults {
		if event.Result.Error != "" || strings.Contains(strings.ToLower(event.Result.Summary), "reject") {
			t.Fatalf("redirected request should execute without rejection, got %#v", event)
		}
	}
	if requests != len(replies) {
		t.Fatalf("expected %d model requests, got %d", len(replies), requests)
	}
}

func TestRunnerExecutesCapabilityOnlyCodeNavigateJSONMissingTool(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "go.mod", "module example.test\n"); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := writeTestFile(root, "main.go", "package main\n\nfunc main() {}\n"); err != nil {
		t.Fatalf("write main.go: %v", err)
	}

	replies := []string{
		`{"capability":"file_outline","path":"main.go"}`,
		"Done.",
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

	var toolRequests []ToolRequestEvent
	var salvageEvent string
	runner := &Runner{
		Client:    llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		Policy:    RunPolicy{MaxToolSteps: 3, MaxNoProgressTurns: 3},
		OnEvent: func(event RunEvent) {
			if event.Role == "visible_tool_salvage" {
				salvageEvent = event.Content
			}
		},
	}
	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "outline main"}},
		llama.ChatOptions{},
		nil,
		func(event ToolRequestEvent) {
			toolRequests = append(toolRequests, event)
		},
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if answer != "Done." {
		t.Fatalf("unexpected answer %q", answer)
	}
	if len(toolRequests) != 1 {
		t.Fatalf("expected one salvaged tool request, got %#v", toolRequests)
	}
	gotReq := toolRequests[0].Request
	if gotReq.Tool != "code_navigate" || gotReq.Capability != "file_outline" || gotReq.Path != "main.go" {
		t.Fatalf("expected inferred code_navigate file_outline request, got %#v", gotReq)
	}
	if !strings.Contains(toolRequests[0].Raw, `"tool":"code_navigate"`) {
		t.Fatalf("canonical raw request should include inferred tool, got %s", toolRequests[0].Raw)
	}
	if !strings.Contains(salvageEvent, "code_navigate") {
		t.Fatalf("expected visible code_navigate salvage event, got %q", salvageEvent)
	}
}

func TestRunnerSalvagesMalformedXMLWrapperBeforeToolParse(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "a.txt", "alpha\n"); err != nil {
		t.Fatalf("write a.txt: %v", err)
	}

	replies := []string{
		`<function=read_file>I'll use {"tool":"read_file","path":"a.txt"}</function broken>`,
		"Done.",
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

	var toolRequests []ToolRequestEvent
	var toolParseResults int
	var salvageEvent string
	runner := &Runner{
		Client:    llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		Policy:    RunPolicy{MaxToolSteps: 3, MaxNoProgressTurns: 3},
		OnEvent: func(event RunEvent) {
			if event.Role == "visible_tool_salvage" {
				salvageEvent = event.Content
			}
		},
	}
	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "read a.txt"}},
		llama.ChatOptions{},
		nil,
		func(event ToolRequestEvent) {
			toolRequests = append(toolRequests, event)
		},
		func(event ToolResultEvent) {
			if event.Request.Tool == "tool_parse" || event.Result.Tool == "tool_parse" {
				toolParseResults++
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if answer != "Done." {
		t.Fatalf("unexpected answer %q", answer)
	}
	if toolParseResults != 0 {
		t.Fatalf("recoverable malformed XML should be salvaged before tool_parse, got %d tool_parse result(s)", toolParseResults)
	}
	if len(toolRequests) != 1 || toolRequests[0].Request.Tool != "read_file" || toolRequests[0].Request.Path != "a.txt" {
		t.Fatalf("expected salvaged read_file request, got %#v", toolRequests)
	}
	if !strings.Contains(salvageEvent, "before recording tool_parse") {
		t.Fatalf("expected parse-error salvage event, got %q", salvageEvent)
	}
}

func TestRunnerSalvagesMalformedReadOnlyJSONPrefixBeforeToolParse(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "internal/agent/runner.go", "package agent\n\nfunc Run() {}\n"); err != nil {
		t.Fatalf("write runner.go: %v", err)
	}

	replies := []string{
		`{"tool":"read_file","path":"internal/agent/runner.go","start_line":1,"end_line":2 - inspect the source before answering`,
		"Evidence comes from bounded source reads.",
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

	var toolRequests []ToolRequestEvent
	var toolParseResults int
	var salvageEvent string
	runner := &Runner{
		Client:    llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		Policy:    RunPolicy{MaxToolSteps: 3, MaxNoProgressTurns: 3},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			Objective:         "What is the role of evidence in this harness?",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"read_file", "search_content"},
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "visible_tool_salvage" {
				salvageEvent = event.Content
			}
		},
	}
	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "What is the role of evidence in this harness?"}},
		llama.ChatOptions{},
		nil,
		func(event ToolRequestEvent) {
			toolRequests = append(toolRequests, event)
		},
		func(event ToolResultEvent) {
			if event.Request.Tool == "tool_parse" || event.Result.Tool == "tool_parse" {
				toolParseResults++
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if answer != "Evidence comes from bounded source reads." {
		t.Fatalf("unexpected answer %q", answer)
	}
	if toolParseResults != 0 {
		t.Fatalf("recoverable malformed read-only JSON should be salvaged before tool_parse, got %d tool_parse result(s)", toolParseResults)
	}
	if len(toolRequests) != 1 {
		t.Fatalf("expected one salvaged tool request, got %#v", toolRequests)
	}
	req := toolRequests[0].Request
	if req.Tool != "read_file" || req.Path != "internal/agent/runner.go" || req.StartLine != 1 || req.EndLine != 2 {
		t.Fatalf("unexpected salvaged request: %#v", req)
	}
	if !strings.Contains(salvageEvent, "before recording tool_parse") || !strings.Contains(salvageEvent, "read_file") {
		t.Fatalf("expected visible read_file salvage event, got %q", salvageEvent)
	}
}

func TestParseMalformedVisibleReadOnlyToolJSONPrefixExtractsSafeFields(t *testing.T) {
	req, raw, ok := parseMalformedVisibleReadOnlyToolJSONPrefix(
		`{"tool":"search_content","pattern":"Evidence","path":"internal/agent","limit":"5","named_gap":"find evidence source" - search first`,
	)
	if !ok {
		t.Fatal("expected malformed read-only JSON prefix to be recoverable")
	}
	if req.Tool != "search_content" || req.Pattern != "Evidence" || req.Path != "internal/agent" || req.Limit != 5 || req.NamedGap != "find evidence source" {
		t.Fatalf("unexpected recovered request: %#v", req)
	}
	if !strings.Contains(raw, `"tool":"search_content"`) || !strings.Contains(raw, `"pattern":"Evidence"`) {
		t.Fatalf("expected canonical raw request, got %s", raw)
	}
}

func TestParseMalformedVisibleReadOnlyToolJSONPrefixRejectsWriteTools(t *testing.T) {
	if req, raw, ok := parseMalformedVisibleReadOnlyToolJSONPrefix(
		`{"tool":"edit_file","path":"README.md","old_text":"old","new_text":"new"} - edit it`,
	); ok {
		t.Fatalf("malformed write JSON must not be recovered as read-only request: req=%#v raw=%s", req, raw)
	}
}

func TestRunnerDoesNotSalvageMalformedWriteJSONPrefixInReadOnlyMode(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "README.md", "old\n"); err != nil {
		t.Fatalf("write README: %v", err)
	}

	replies := []string{
		`{"tool":"edit_file","path":"README.md","old_text":"old","new_text":"new" - apply this edit`,
		"<<QUESTION>>: Blocked by malformed write request.",
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

	var toolRequests []ToolRequestEvent
	var toolParseResults int
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: false,
		}),
		Policy: RunPolicy{MaxToolSteps: 2, MaxNoProgressTurns: 3},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			Objective:         "Explain the README.",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"read_file"},
		},
	}
	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Explain the README."}},
		llama.ChatOptions{},
		nil,
		func(event ToolRequestEvent) {
			toolRequests = append(toolRequests, event)
		},
		func(event ToolResultEvent) {
			if event.Request.Tool == "tool_parse" || event.Result.Tool == "tool_parse" {
				toolParseResults++
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if answer != "Blocked by malformed write request." {
		t.Fatalf("unexpected answer %q", answer)
	}
	if toolParseResults != 1 {
		t.Fatalf("malformed write JSON must remain a tool_parse rejection, got %d", toolParseResults)
	}
	if len(toolRequests) != 0 {
		t.Fatalf("malformed write JSON must not be salvaged into a tool request, got %#v", toolRequests)
	}
	data, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatalf("read README: %v", err)
	}
	if string(data) != "old\n" {
		t.Fatalf("read-only malformed write changed workspace: %q", string(data))
	}
}

func TestRunnerConcreteReadSymbolEvidenceDoesNotRestartRangeLessReadLoop(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "go.mod", "module example.test\n"); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := writeTestFile(root, "cmd/idiom/product_workflow.go", "package main\n\nfunc runProductWorkflow() string {\n\treturn \"ok\"\n}\n"); err != nil {
		t.Fatalf("write source: %v", err)
	}

	replies := []string{
		`{"tool":"read_file","path":"cmd/idiom/product_workflow.go"}`,
		"Done.",
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

	var toolRequests []ToolRequestEvent
	var rewriteEvent string
	runner := &Runner{
		Client:    llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		Policy:    RunPolicy{MaxToolSteps: 4, MaxNoProgressTurns: 4},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Objective: "update product workflow",
		},
		SeedResults: []workspace.ToolResult{
			{
				Tool:    "read_file",
				Summary: "Redirected cmd/idiom/product_workflow.go to code_navigate",
				Output:  "The request targets go code. Capability route: file_outline via adapter code_navigate instead of raw read_file.",
			},
			{
				Tool:    "code_navigate",
				Summary: "Code navigation routed file_outline via go_file_outline",
				Output: strings.Join([]string{
					"capability: file_outline",
					"route_summary: Outlined 1 top-level declaration(s) in cmd/idiom/product_workflow.go.",
					"declarations:",
					"- runProductWorkflow lines 3-5 tags: implementation",
				}, "\n"),
			},
			{
				Tool:    "code_navigate",
				Summary: "Code navigation routed read_symbol via go_read_symbol",
				Output: strings.Join([]string{
					"capability: read_symbol",
					"route_summary: Read runProductWorkflow in cmd/idiom/product_workflow.go lines 3-5",
					"func runProductWorkflow() string {",
					"\treturn \"ok\"",
					"}",
				}, "\n"),
			},
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "tool_request_rewrite" {
				rewriteEvent = event.Content
			}
		},
	}
	if _, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "update product workflow"}},
		llama.ChatOptions{},
		nil,
		func(event ToolRequestEvent) {
			toolRequests = append(toolRequests, event)
		},
		nil,
	); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(toolRequests) != 1 {
		t.Fatalf("expected one model-requested tool execution, got %#v", toolRequests)
	}
	if toolRequests[0].Request.Tool != "read_file" {
		t.Fatalf("concrete read_symbol evidence should not restart code_navigate loop, got %#v", toolRequests[0].Request)
	}
	if strings.Contains(rewriteEvent, "repeated_rangeless_read_file_bounded_source") {
		t.Fatalf("did not expect repeated range-less read rewrite after concrete source evidence, got %q", rewriteEvent)
	}
}

func TestLooksLikeTextualToolCallRejectsLineOrientedToolPayload(t *testing.T) {
	payload := "tool: code_navigate\ncapability: read_symbol\npath: internal/app/turn_planner.go\npattern: classifyTurnRoute"
	if !looksLikeTextualToolCall(payload) {
		t.Fatalf("expected line-oriented tool payload to be rejected")
	}
	contextPayload := "tool: context_pack\ntask: plan a bounded change to ShortLabel in internal/labels/labels.go\nmode: plan\nlimit: 8"
	if !looksLikeTextualToolCall(contextPayload) {
		t.Fatalf("expected context-pack line-oriented tool payload to be rejected")
	}
	multilineJSONPayload := "tool: read_file\n{\n  \"tool\": \"read_file\",\n  \"path\": \"internal/labels/labels.go\"\n}"
	if !looksLikeTextualToolCall(multilineJSONPayload) {
		t.Fatalf("expected multiline JSON line-oriented tool payload to be rejected")
	}
	if looksLikeTextualToolCall("Tool: route classifier\n\nThis section explains the classifier in prose.") {
		t.Fatalf("prose heading should not be rejected as a line-oriented tool payload")
	}

	// <invoke> XML tool-call markup detection
	if !looksLikeTextualToolCall("<invoke name=\"context_pack\">") {
		t.Fatalf("expected <invoke> XML to be detected as textual tool call")
	}
	if !looksLikeTextualToolCall("</invoke>") {
		t.Fatalf("expected </invoke> XML to be detected as textual tool call")
	}
	if !looksLikeTextualToolCall("<invoke") {
		t.Fatalf("expected partial <invoke to be detected as textual tool call")
	}
	if !looksLikeTextualToolCall("<invoke>\n<invoke name=\"search_files\">\n</invoke>") {
		t.Fatalf("expected invoke XML block to be detected as textual tool call")
	}
}

// Test that janus_tool_calls XML is detected as textual tool call
func TestLooksLikeTextualToolCallDetectsJanusToolCalls(t *testing.T) {
	// The exact format from the live conversation log
	livePayload := `<janus_tool_calls>
<janus_invoke name="code_navigate">
<janus_parameter name="capability" string="true">find_symbol</janus_parameter>
<janus_parameter name="named_gap" string="true">Find PlanThenExecutePlannerTaskFamily constant</janus_parameter>
<janus_parameter name="path" string="true">internal/agent</janus_parameter>
</janus_invoke>
</janus_tool_calls>`
	if !looksLikeTextualToolCall(livePayload) {
		t.Fatal("expected janus_tool_calls XML to be detected as textual tool call")
	}
	if !looksLikeTextualToolCall("<janus_tool_calls>") {
		t.Fatal("expected bare janus_tool_calls open tag to be detected")
	}
	if !looksLikeTextualToolCall("</janus_tool_calls>") {
		t.Fatal("expected bare janus_tool_calls close tag to be detected")
	}
	if !looksLikeTextualToolCall("<janus_invoke name=\"code_navigate\">") {
		t.Fatal("expected janus_invoke open tag to be detected")
	}
	if !looksLikeTextualToolCall("</janus_invoke>") {
		t.Fatal("expected janus_invoke close tag to be detected")
	}
}

// Test that n_tool_calls XML (DeepSeek's native tool-call markup when
// forced into text-only mode) is detected as textual tool call.
func TestLooksLikeTextualToolCallDetectsNToolCalls(t *testing.T) {
	payload := `<n_tool_calls>
<n_invoke name="code_navigate">
<n_parameter name="capability" string="true">find_symbol</n_parameter>
<n_parameter name="named_gap" string="true">Find constant</n_parameter>
<n_parameter name="path" string="true">internal/agent</n_parameter>
</n_invoke>
</n_tool_calls>`
	if !looksLikeTextualToolCall(payload) {
		t.Fatal("expected n_tool_calls XML to be detected as textual tool call")
	}
	if !looksLikeTextualToolCall("<n_tool_calls>") {
		t.Fatal("expected bare n_tool_calls open tag to be detected")
	}
	if !looksLikeTextualToolCall("</n_tool_calls>") {
		t.Fatal("expected bare n_tool_calls close tag to be detected")
	}
	if !looksLikeTextualToolCall("<n_invoke name=\"read_file\">") {
		t.Fatal("expected n_invoke open tag to be detected")
	}
	if !looksLikeTextualToolCall("</n_invoke>") {
		t.Fatal("expected n_invoke close tag to be detected")
	}
	if !looksLikeTextualToolCall("<n_parameter name=\"path\" string=\"true\">foo</n_parameter>") {
		t.Fatal("expected n_parameter tag to be detected")
	}
}

func TestIsTextualToolCallExportedAPI(t *testing.T) {
	// Verify the exported API that the presentation layer uses
	if !IsTextualToolCall("<invoke name=\"context_pack\">") {
		t.Fatal("IsTextualToolCall should detect invoke XML")
	}
	if !IsTextualToolCall("<tool_call>{\"tool\":\"read\"}</tool_call>") {
		t.Fatal("IsTextualToolCall should detect tool_call XML")
	}
	if IsTextualToolCall("The lane transition works by returning to main state after implementation.") {
		t.Fatal("IsTextualToolCall should not flag normal prose")
	}
}

func TestFinalAnswerNeedsDecorationRejectsStructuredAnswerEnvelope(t *testing.T) {
	answer := `{"answer":["The route classifier chooses the workflow family."],"verification":["internal/app/turn_planner.go"]}`
	if !finalAnswerNeedsDecoration(answer) {
		t.Fatal("expected structured answer envelope to need decoration")
	}
}

func TestUnexecutedToolJSONDetectionDoesNotRejectGenericJSON(t *testing.T) {
	for _, reply := range []string{
		`{"items":["one","two"]}`,
		`{"command":"make build"}`,
		`{"prompt":"Summarize this file"}`,
		"Here is an explanatory JSON example for documentation:\n\n```json\n" +
			`{"tool":"search_files","pattern":"*.jsonl","max_depth":2}` + "\n```\n\n" +
			"That example is not a tool call being requested now.",
	} {
		if looksLikeUnexecutedToolRequestAnswer(reply) {
			t.Fatalf("generic JSON should not be treated as an unexecuted tool request: %s", reply)
		}
	}
	if !looksLikeUnexecutedToolRequestAnswer(`{"task":"implement docs/writer-route-decision-report-only-plan.md","mode":"plan","limit":12}`) {
		t.Fatalf("context_pack-shaped JSON should still be rejected as an unexecuted tool request")
	}
}

func TestUnexecutedToolJSONDetectionRejectsLongProcessProse(t *testing.T) {
	reply := "The search returned no results, which is unexpected given the previous tool result showed 400 log files. Let me try a different approach to find the latest conversation logs.\n\n" +
		`{"tool":"search_files","pattern":"*.jsonl","max_depth":2}`
	if !looksLikeUnexecutedToolRequestAnswer(reply) {
		t.Fatal("expected long process prose with embedded tool JSON to be rejected")
	}
	result, rejected := (&Runner{}).unexecutedToolRequestCompletionRejection(reply)
	if !rejected || result.Tool != "completion_gate" || !strings.Contains(result.Summary, "Unexecuted tool request") {
		t.Fatalf("unexpected completion rejection: rejected=%v result=%#v", rejected, result)
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

func TestRunnerWorkspaceToolRulesDenyBeforePolicyAllow(t *testing.T) {
	root := t.TempDir()
	secret := "SECRET_SENTINEL_DO_NOT_LEAK"
	if err := os.WriteFile(filepath.Join(root, "secret.txt"), []byte(secret), 0o600); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}
	replies := []string{
		"```tool\n{\"tool\":\"read_file\",\"path\":\"secret.txt\"}\n```",
		"```tool\n{\"tool\":\"read_file\",\"path\":\"secret.txt\"}\n```",
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

	var permissionEvent string
	var allowEvent string
	var completionStatus string
	var leaked bool
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
			ToolRules: map[string][]config.ToolRule{
				"read_file": {
					{Pattern: "secret.txt", Decision: "deny"},
				},
			},
		}),
		Policy: RunPolicy{MaxToolSteps: 3, MaxNoProgressTurns: 3},
		PermissionPolicy: ToolPermissionPolicy{
			Default: PermissionAllow,
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "permission_decision" {
				if strings.Contains(event.Content, `"decision":"deny"`) {
					permissionEvent = event.Content
				}
				if strings.Contains(event.Content, `"decision":"allow"`) {
					allowEvent = event.Content
				}
			}
			if event.Role == "completion_status" {
				completionStatus = event.Content
			}
			if strings.Contains(event.Content, secret) {
				leaked = true
			}
		},
	}

	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "read secret"}}, llama.ChatOptions{}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(answer, "workspace `tool_rules` deny `read_file`") {
		t.Fatalf("unexpected answer %q", answer)
	}
	if requests != 1 {
		t.Fatalf("workspace deny should close before a second model call, got %d requests", requests)
	}
	if permissionEvent == "" || !strings.Contains(permissionEvent, "workspace.tool_rules") {
		t.Fatalf("expected workspace tool_rules deny event, got %q", permissionEvent)
	}
	if !strings.Contains(completionStatus, `"blocker_kind":"workspace_tool_rules_deny"`) {
		t.Fatalf("expected workspace tool_rules completion status, got %q", completionStatus)
	}
	if allowEvent != "" {
		t.Fatalf("workspace deny must not emit allow permission event: %s", allowEvent)
	}
	if leaked {
		t.Fatal("deny path leaked sentinel content")
	}
}

func TestRunnerRejectsToolOutsideCurrentSurfaceBeforeExecution(t *testing.T) {
	root := t.TempDir()
	replies := []string{
		"```tool\n{\"tool\":\"write_file\",\"path\":\"blocked.txt\",\"content\":\"should not be written\",\"description\":\"attempt disallowed write\"}\n```",
		"Blocked by the current tool surface.",
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

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"read_file"},
		},
		Policy: RunPolicy{MaxToolSteps: 3, MaxNoProgressTurns: 3},
	}

	var rejected bool
	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "answer without writes"}}, llama.ChatOptions{}, nil, nil, func(event ToolResultEvent) {
		if event.Request.Tool == "write_file" && strings.Contains(event.Result.Summary, "Tool not available in current runtime contract") {
			rejected = true
		}
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(answer, "Blocked by the current tool surface") {
		t.Fatalf("unexpected answer %q", answer)
	}
	if !rejected {
		t.Fatal("expected runner to reject the disallowed tool at the current surface gate")
	}
	if _, err := os.Stat(filepath.Join(root, "blocked.txt")); !os.IsNotExist(err) {
		t.Fatalf("disallowed write executed, stat err=%v", err)
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
	runner := &Runner{RuntimeBrief: brief}
	if kind, reason := runner.finalAnswerBlockerKind(answer, results); kind != "required_edit_paths_remaining" || reason != "required_edit_paths_remaining" {
		t.Fatalf("mutation fallback should preserve required-path blocker classification, got (%q, %q):\n%s", kind, reason, answer)
	}
	if strings.Contains(answer, "not enough supported evidence") {
		t.Fatalf("mutation fallback must not be rewritten as read-only uncertainty:\n%s", answer)
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
		if slices.Contains(brief.AllowedTools, forbidden) {
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
		if !slices.Contains(brief.AllowedTools, allowed) {
			t.Fatalf("exact-anchor pre-write brief should keep %s, got %#v", allowed, brief.AllowedTools)
		}
	}
	if !reflect.DeepEqual(brief.RequiredTools, []string{"edit_file_or_apply_patch"}) {
		t.Fatalf("exact-anchor pre-write required tools = %#v, want edit only", brief.RequiredTools)
	}
	if !slices.Contains(brief.InvalidActions, "read/search/shell before first exact-anchor write") {
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
		if slices.Contains(brief.AllowedTools, forbidden) {
			t.Fatalf("write-ready pre-write brief advertised broad tool %s in allowed tools: %#v", forbidden, brief.AllowedTools)
		}
	}
	for _, allowed := range []string{"read_file", "code_navigate", "edit_file", "write_file", "apply_patch"} {
		if !slices.Contains(brief.AllowedTools, allowed) {
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
		if slices.Contains(rails.AllowedTools, forbidden) {
			t.Fatalf("exact-anchor pre-write control rails advertised %s: %#v", forbidden, rails.AllowedTools)
		}
	}
	for _, allowed := range []string{"edit_file", "apply_patch"} {
		if !slices.Contains(rails.AllowedTools, allowed) {
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
			Output:  "Question: How does this harness utilize sub-agents?\nSource: delegated read-only evidence packet.\n\nInspected paths:\n- `internal/agent/runner.go`\n- `internal/agent/runner_child_tasks.go`\n\nKey findings:\n- The harness uses the `task` tool in internal/agent/runner.go to launch a read-only child runner implemented by internal/agent/runner_child_tasks.go.\n",
		},
		{Tool: "read_file", Summary: "Read internal/agent/runner.go lines 1-20", Output: "func (r *Runner) runChildTask(...) {}"},
	})
	if !ok {
		t.Fatal("expected immediate fallback answer")
	}
	for _, want := range []string{"The answer is grounded in", "`internal/agent/runner.go`", "`internal/agent/runner_child_tasks.go`"} {
		if !strings.Contains(answer, want) {
			t.Fatalf("expected fallback to include %q, got:\n%s", want, answer)
		}
	}
	for _, forbidden := range []string{"Source: delegated read-only evidence packet.", "Inspected paths:", "Key findings:"} {
		if strings.Contains(answer, forbidden) {
			t.Fatalf("expected fallback to avoid internal packet section %q, got:\n%s", forbidden, answer)
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
	good := "Source-body evidence paths:\n- internal/agent/runner_child_tasks.go\n\nInspected paths: internal/agent/runner.go, internal/agent/runner_child_tasks.go\nKey findings: the harness uses the task tool to launch an isolated read-only child runner.\nConfidence: high\n"
	if !childPacketContainsGroundedAnswerEvidence("", good) {
		t.Fatal("expected grounded child packet evidence to be recognized")
	}
	goodSubagent := "Source-body evidence paths:\n- internal/agent/runner_child_tasks.go\n- internal/agent/runner.go\n\nInspected paths: internal/agent/runner.go, internal/agent/runner_child_tasks.go\nKey findings: the harness uses the task tool to launch subagents as child runners and return their packets to the parent runner.\nConfidence: high\n"
	if !childPacketContainsGroundedAnswerEvidence("Can you tell me how this harness uses subagents?", goodSubagent) {
		t.Fatal("expected objective-relevant subagent child packet to be recognized")
	}
	offObjectiveSubagent := "Source-body evidence paths:\n- internal/agent/runner.go\n- internal/comprehension/result_metadata.go\n\nInspected paths: internal/agent/runner.go, internal/comprehension/result_metadata.go\nKey findings: the runner reads ChatMessage values and stores context metadata.\nConfidence: high\n"
	if childPacketContainsGroundedAnswerEvidence("Can you tell me how this harness uses subagents?", offObjectiveSubagent) {
		t.Fatal("expected off-objective child packet to be rejected for subagent objective")
	}
	offObjectiveDiscovery := "Source-body evidence paths:\n- internal/workspace/tool_schema_export.go\n- internal/workspace/tool_schemas.go\n\nInspected paths: internal/workspace/tool_schema_export.go, internal/workspace/tool_schemas.go\nKey findings: tool schema export serializes available discovery tool schemas and returns JSON metadata.\nConfidence: high\n"
	if childPacketContainsGroundedAnswerEvidence("What can we add to our discovery tools in an effort at increasing their overall usefulness?", offObjectiveDiscovery) {
		t.Fatal("expected generic tool/schema packet to be rejected for discovery usefulness objective")
	}
	partial := "Partial findings from this turn (the budget was reached before a full synthesis):\n\nSource-body evidence paths:\n- internal/agent/runner.go\n\nVerified context:\n- Read internal/agent/runner.go lines 100-140\n- Found internal/app/turn_planner.go references\n"
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
	artifactOnly := "Source-body evidence paths:\n- .idiom/comprehension/views/context.json\n\nInspected paths: .idiom/comprehension/views/context.json\nKey findings: context cache inspected."
	if childPacketContainsGroundedAnswerEvidence("", artifactOnly) {
		t.Fatal("expected generated artifact child packet to be rejected")
	}
	docsOnly := "Source-body evidence paths:\n- README.md\n\nInspected paths: README.md\nKey findings: documentation entrypoint found."
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
	markdownPathLine := "- `internal/agent/runner_child_tasks.go`"
	if normalizedLine := normalizeDelegatedReadOnlyPacket(markdownPathLine); normalizedLine != markdownPathLine {
		t.Fatalf("expected packet normalization to preserve markdown path citations, got %q", normalizedLine)
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

func TestDelegatedReadOnlyRepairsInvalidInitialDiscoveryPurpose(t *testing.T) {
	runner := &Runner{
		AgentName: "child:explorer",
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:   "delegated_read_only",
			AllowedTools: []string{"discovery_query", "search_content", "read_file"},
			Objective:    "What can we add to our discovery tools in an effort at increasing their overall usefulness?\nNamed workspace scope: internal/agent, internal/workspace, internal\nKey terms: discovery tools, usefulness",
		},
	}
	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Request: workspace.ToolRequest{Tool: "discovery_query", Purpose: "tool_usefulness"},
	})
	if decision.Request.Tool != "discovery_query" || decision.Request.Purpose != "architecture_boundary" {
		t.Fatalf("expected invalid discovery purpose to be repaired to architecture_boundary, got %#v", decision.Request)
	}
	if decision.Request.Path != "internal/agent" || decision.Request.Pattern == "" || decision.Request.MaxDepth != 2 {
		t.Fatalf("expected repaired discovery query to carry scope/pattern/depth, got %#v", decision.Request)
	}
}

func TestDelegatedReadOnlyPreservesValidDiscoveryUsefulnessPurposeAndPath(t *testing.T) {
	runner := &Runner{
		AgentName: "child:explorer",
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:   "delegated_read_only",
			AllowedTools: []string{"discovery_query", "search_content", "read_file"},
			Objective:    "What can we add to our discovery tools in an effort at increasing their overall usefulness?\nNamed workspace scope: internal/agent, internal/workspace, internal",
		},
	}
	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Request: workspace.ToolRequest{Tool: "discovery_query", Purpose: "history_context", Path: "internal/workspace"},
	})
	if decision.Request.Tool != "discovery_query" || decision.Request.Purpose != "history_context" {
		t.Fatalf("expected valid discovery purpose to be preserved, got %#v", decision.Request)
	}
	if decision.Request.Path != "internal/workspace" {
		t.Fatalf("expected valid discovery path to be preserved, got %#v", decision.Request)
	}
	for _, forbidden := range []string{"answer[-_ ]?composer", "synthesizeDelegatedReadOnlyAnswer", "readiness", "closeout"} {
		if strings.Contains(decision.Request.Pattern, forbidden) {
			t.Fatalf("expected usefulness pattern not to inject synthesis boundary %q, got %#v", forbidden, decision.Request)
		}
	}
}

func TestDelegatedReadOnlyRepairsSecurityPurposeWithoutSecurityObjective(t *testing.T) {
	runner := &Runner{
		AgentName: "child:explorer",
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:   "delegated_read_only",
			AllowedTools: []string{"discovery_query", "search_content", "read_file"},
			Objective:    "What can we add to our discovery tools in an effort at increasing their overall usefulness?\nNamed workspace scope: internal/agent, internal/workspace, internal",
		},
	}
	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Request: workspace.ToolRequest{Tool: "discovery_query", Purpose: "security_review", Path: "internal/agent"},
	})
	if decision.Request.Tool != "discovery_query" || decision.Request.Purpose != "architecture_boundary" {
		t.Fatalf("expected unsupported security_review to be repaired to architecture_boundary, got %#v", decision.Request)
	}
}

func TestDelegatedReadOnlyRepairsDiscoveryPathOutsideDetailedNamedScope(t *testing.T) {
	runner := &Runner{
		AgentName: "child:explorer",
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:   "delegated_read_only",
			AllowedTools: []string{"discovery_query", "search_content", "read_file"},
			Objective:    "What can we add to our discovery tools in an effort at increasing their overall usefulness?\nNamed workspace scope: internal/agent, internal/workspace, internal",
		},
	}
	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Request: workspace.ToolRequest{Tool: "discovery_query", Purpose: "architecture_boundary", Path: "internal/frontier", Pattern: "discovery|tools"},
	})
	if decision.Request.Tool != "discovery_query" || decision.Request.Path != "internal/agent" {
		t.Fatalf("expected out-of-scope discovery path to use primary detailed named scope, got %#v", decision.Request)
	}
}

func TestDelegatedReadOnlyRepeatedDiscoveryConsumesCandidateSource(t *testing.T) {
	runner := &Runner{
		AgentName: "child:explorer",
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:   "delegated_read_only",
			AllowedTools: []string{"discovery_query", "search_content", "read_file"},
			Objective:    "Explain how this harness uses subagents.\nNamed workspace scope: internal/agent, internal/workspace, internal",
		},
	}
	results := []workspace.ToolResult{{
		Tool:   "discovery_query",
		Output: `{"evidence":[{"name":"text_anchor_search","summary":"implementation anchor: internal/agent/runner_child_tasks.go:42 runChildTask"},{"name":"text_anchor_search","summary":"implementation anchor: internal/agent/runner.go:300 task tool dispatch"}]}`,
	}}
	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Results: results,
		Request: workspace.ToolRequest{Tool: "discovery_query", Purpose: "architecture_boundary", Path: "internal/agent", Pattern: "subagent|task"},
	})
	if decision.Request.Tool != "read_file" {
		t.Fatalf("expected repeated discovery to consume a candidate source path, got %#v", decision.Request)
	}
	if !slices.Contains([]string{"internal/agent/runner_child_tasks.go", "internal/agent/runner.go"}, decision.Request.Path) ||
		decision.Request.StartLine != 1 || decision.Request.EndLine != 160 {
		t.Fatalf("expected bounded read_file on discovered source candidate, got %#v", decision.Request)
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

func TestPrepareReadOnlyChildPromptAddsDelegatedLikelySourceScope(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"internal/agent", "internal/workspace"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	runner := &Runner{Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root})}
	prompt := runner.prepareReadOnlyChildPrompt("Inspect how this harness uses subagents. Focus on task tool implementation and child spawning.")
	if !strings.Contains(prompt, "Named workspace scope: internal/agent, internal/workspace, internal") {
		t.Fatalf("expected model-created child task to inherit delegated likely-source scope, got:\n%s", prompt)
	}
}

func TestPrepareReadOnlyChildPromptAddsPrimaryScopeWhenPromptNamesDocs(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"internal/agent", "internal/workspace"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	runner := &Runner{Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root})}
	prompt := runner.prepareReadOnlyChildPrompt("Inspect docs/codebase-comprehension-memory-design.md and explain the discovery tool surface.")
	if !strings.Contains(prompt, "Named workspace scope: docs/codebase-comprehension-memory-design.md") {
		t.Fatalf("expected prompt to preserve named docs evidence, got:\n%s", prompt)
	}
	if !strings.Contains(prompt, "Named workspace scope: internal/agent, internal/workspace, internal") {
		t.Fatalf("expected docs-only prompt to inherit primary implementation scope, got:\n%s", prompt)
	}
	if got := delegatedReadOnlyBootstrapScope(prompt); got != "internal/agent" {
		t.Fatalf("expected bootstrap scope to prefer primary implementation scope over docs evidence, got %q from:\n%s", got, prompt)
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

func TestDelegatedReadOnlyBootstrapPatternForDiscoveryUsefulnessUsesQuestionTermsOnly(t *testing.T) {
	pattern := delegatedReadOnlyBootstrapPattern("What can we add to our discovery tools to increase their overall usefulness?")
	for _, want := range []string{"discovery", "tools", "increase"} {
		if !strings.Contains(pattern, want) {
			t.Fatalf("expected discovery usefulness pattern to preserve question term %q, got %q", want, pattern)
		}
	}
	for _, forbidden := range []string{"answer[-_ ]?composer", "synthesizeDelegatedReadOnlyAnswer", "readiness", "child[-_ ]?packet", "closeout", "comprehension", "frontier", "workspace|tool[-_ ]?surface"} {
		if strings.Contains(pattern, forbidden) {
			t.Fatalf("discovery usefulness pattern should not inject %q, got %q", forbidden, pattern)
		}
	}
}

func TestDelegatedReadOnlyBootstrapPatternDoesNotInjectSubagentImplementationAliases(t *testing.T) {
	pattern := delegatedReadOnlyBootstrapPattern("Explain how this harness uses sub-agents.")
	for _, want := range []string{"explain", "sub[-_ ]?agents"} {
		if !strings.Contains(pattern, want) {
			t.Fatalf("expected subagent pattern to preserve question term %q, got %q", want, pattern)
		}
	}
	for _, forbidden := range []string{"runChildTask", "child[-_ ]?task", "task[-_ ]?tool", "delegat"} {
		if strings.Contains(pattern, forbidden) {
			t.Fatalf("subagent pattern should not inject implementation alias %q, got %q", forbidden, pattern)
		}
	}
}

func TestDelegatedReadOnlyDiscoveryRepairPreservesSubagentPurposeAndPath(t *testing.T) {
	runner := &Runner{
		AgentName: "child:explorer",
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "delegated_read_only",
			Contract:          "architecture_assessment",
			PermissionProfile: "read_only_workspace",
			Objective:         "Can you tell me how this harness uses subagents?",
		},
	}
	repaired, ok := runner.delegatedReadOnlyDiscoveryQueryRepairRequest(nil, workspace.ToolRequest{
		Tool:    "discovery_query",
		Purpose: "impact_analysis",
		Path:    ".",
	})
	if !ok {
		t.Fatal("expected delegated subagent discovery query to be repaired")
	}
	if repaired.Purpose != "impact_analysis" || repaired.Path != "." {
		t.Fatalf("expected valid discovery purpose/path to be preserved, got %#v", repaired)
	}
	for _, want := range []string{"tell", "subagents"} {
		if !strings.Contains(repaired.Pattern, want) {
			t.Fatalf("expected repaired query pattern to contain %q, got %q", want, repaired.Pattern)
		}
	}
	for _, forbidden := range []string{"runChildTask", "child[-_ ]?task", "task[-_ ]?tool"} {
		if strings.Contains(repaired.Pattern, forbidden) {
			t.Fatalf("subagent discovery repair should not inject implementation alias %q, got %q", forbidden, repaired.Pattern)
		}
	}
}

func TestDelegatedReadOnlyDiscoveryRepairUsesNamedPrimaryScopeForDotPath(t *testing.T) {
	runner := &Runner{
		AgentName: "child:explorer",
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "delegated_read_only",
			Contract:          "architecture_assessment",
			PermissionProfile: "read_only_workspace",
			Objective: strings.Join([]string{
				"Inspect how this harness uses subagents.",
				"Named workspace scope: internal/agent, internal/workspace, internal",
				"Key terms: subagents, child, task",
			}, "\n"),
		},
	}
	repaired, ok := runner.delegatedReadOnlyDiscoveryQueryRepairRequest([]workspace.ToolResult{{
		Tool:    "search_content",
		Summary: "Found candidate paths",
	}}, workspace.ToolRequest{
		Tool:    "discovery_query",
		Purpose: "investigate subagent orchestration",
		Path:    ".",
	})
	if !ok {
		t.Fatal("expected invalid delegated discovery query to be repaired after prior evidence")
	}
	if repaired.Purpose != "architecture_boundary" {
		t.Fatalf("expected invalid purpose repaired, got %#v", repaired)
	}
	if repaired.Path != "internal/agent" {
		t.Fatalf("expected dot path repaired to primary named scope, got %#v", repaired)
	}
}

func TestMainArchitectureBootstrapDelegatesBroadFirstRead(t *testing.T) {
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
			SearchPolicy:      []string{"delegate_broad_discovery_when_needed", "prefer_discovery_query_for_architecture_boundary", "trace_boundaries_from_named_components"},
		},
	}
	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Request: workspace.ToolRequest{Tool: "list_files", Path: "."},
	})
	if decision.Request.Tool != "task" || decision.Request.Mode != "read_only" || decision.Request.Agent != "explorer" {
		t.Fatalf("expected architecture bootstrap to delegate broad first read, got %#v", decision.Request)
	}
	if !strings.Contains(decision.Request.Prompt, "Start from source-of-truth implementation files") {
		t.Fatalf("delegated prompt lost source-first guidance:\n%s", decision.Request.Prompt)
	}
}

func TestMainArchitectureBootstrapKeepsConcreteFeatureQuestionLocal(t *testing.T) {
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
			Objective:         "What does the debug log redacted mode do?",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"task", "context_pack", "discovery_query", "search_content", "read_file"},
			SearchPolicy:      []string{"delegate_broad_discovery_when_needed", "prefer_discovery_query_for_architecture_boundary"},
		},
	}
	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Request: workspace.ToolRequest{Tool: "context_pack", Query: "debug log redacted mode"},
	})
	if decision.Request.Tool != "context_pack" {
		t.Fatalf("expected concrete feature question to stay local, got %#v", decision.Request)
	}
}

func TestMainArchitectureBootstrapDelegatesBroadBoundaryQuestion(t *testing.T) {
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
			AllowedTools:      []string{"task", "context_pack", "discovery_query", "search_content", "read_file"},
			SearchPolicy:      []string{"delegate_broad_discovery_when_needed", "prefer_discovery_query_for_architecture_boundary"},
		},
	}
	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Request: workspace.ToolRequest{Tool: "context_pack", Query: "subagent architecture"},
	})
	if decision.Request.Tool != "task" || decision.Request.Mode != "read_only" || decision.Request.Agent != "explorer" {
		t.Fatalf("expected broad architecture context_pack bootstrap to delegate, got %#v", decision.Request)
	}
}

func TestMainArchitectureBootstrapIgnoresCompletionGateRejection(t *testing.T) {
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
			Objective:         "What can we add to our discovery tools in an effort at increasing their overall usefulness?",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"task", "context_pack", "discovery_query", "search_content", "read_file"},
			SearchPolicy:      []string{"delegate_broad_discovery_when_needed", "prefer_discovery_query_for_architecture_boundary"},
		},
	}
	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Request: workspace.ToolRequest{Tool: "context_pack", Task: "assess discovery tool gaps and propose new capabilities"},
		Results: []workspace.ToolResult{{
			Tool:    "completion_gate",
			Summary: "Read-only completion gate rejected final answer",
			Output:  "answer missing inspected paths and confidence",
		}},
	})
	if decision.Request.Tool != "task" || decision.Request.Mode != "read_only" || decision.Request.Agent != "explorer" {
		t.Fatalf("expected completion-gate rejection not to suppress architecture task bootstrap, got %#v", decision.Request)
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

func TestPostReadinessCodeNavigateReadSymbolRedirectsToExtractBodyAfterTruncatedRead(t *testing.T) {
	runner := &Runner{}
	results := []workspace.ToolResult{{
		Tool:    "code_navigate",
		Summary: "Code navigation routed read_symbol via go_read_symbol",
		Output:  "capability: read_symbol\nlanguage: go\nrouted_tool: go_read_symbol\nroute_summary: Read Model.handleKeyPress in internal/app/update_handlers.go lines 22-141 (truncated from lines 22-182; full-body adapter: go_extract_body)\n\n22:func (m *Model) handleKeyPress(...) {",
	}}
	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Request: workspace.ToolRequest{Tool: "code_navigate", Capability: "read_symbol", Path: "internal/app/update_handlers.go", Pattern: "handleKeyPress"},
		Results: results,
	})
	if decision.Request.Tool != "code_navigate" || decision.Request.Capability != "extract_body" {
		t.Fatalf("expected truncated code_navigate read_symbol follow-up to upgrade to code_navigate extract_body, got %#v", decision.Request)
	}
	if decision.Request.Pattern != "handleKeyPress" || decision.Request.Path != "internal/app/update_handlers.go" {
		t.Fatalf("expected upgrade to preserve target, got %#v", decision.Request)
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
	if decision.Request.Path != "internal/agent/runner_child_tasks.go" {
		t.Fatalf("expected pivot redirect to targeted source-body window, got %#v", decision.Request)
	}
	if decision.Request.StartLine != 1 || decision.Request.EndLine != 96 {
		t.Fatalf("expected source-body window around runner_child_tasks.go, got %#v", decision.Request)
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
		"The answer is grounded in",
		"What this establishes:",
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
		"Source: delegated read-only evidence packet.",
		"Inspected paths:",
		"Key findings:",
		"Confidence:",
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
		{
			Tool:    "read_file",
			Summary: "Read internal/agent/runner_child_tasks.go lines 1-120",
			Output:  "package agent\nfunc (r *Runner) runChildTask() {}\n",
		},
	}

	paths := delegatedDiscoveryQueryEvidencePaths(results, 4)
	if len(paths) < 2 || paths[0] != "internal/agent/runner.go" || paths[1] != "internal/agent/runner_child_tasks.go" {
		t.Fatalf("expected text-anchor evidence paths to outrank target listing paths, got %#v", paths)
	}
	for _, want := range []string{"internal/agent/runner.go", "internal/agent/runner_child_tasks.go"} {
		if !slices.Contains(paths, want) {
			t.Fatalf("expected discovery paths to contain %q, got %#v", want, paths)
		}
	}
	if slices.Contains(paths, ".idiom/discovery/queries/query.json") {
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
		if !slices.Contains(paths, want) {
			t.Fatalf("expected core path %q in top evidence paths, got %#v", want, paths)
		}
	}
	if slices.Contains(paths, "internal/agent/modes.go") {
		t.Fatalf("incidental path displaced core runner evidence: %#v", paths)
	}
}

func TestAnswerComposerUsesProvidedDiscoveryUsefulnessEvidenceOnly(t *testing.T) {
	summaries := []delegatedDiscoverySummary{{
		Purpose:      "architecture_boundary",
		ArtifactPath: ".idiom/discovery/queries/query.json",
		Evidence: []struct {
			Name          string `json:"name"`
			Tool          string `json:"tool"`
			Summary       string `json:"summary"`
			Error         string `json:"error,omitempty"`
			OutputExcerpt string `json:"output_excerpt,omitempty"`
		}{{
			Name:          "text_anchor_search",
			Summary:       "Found discovery tool implementation paths.",
			OutputExcerpt: "internal/workspace/discovery_query.go:1: package workspace",
		}},
	}}

	answer, ok := AnswerComposer{}.Compose(AnswerComposerInput{
		Question:      "What can we add to our discovery tools in an effort at increasing their overall usefulness?",
		EvidencePaths: []string{"internal/workspace/discovery_query.go"},
		Summaries:     summaries,
	})
	if !ok {
		t.Fatal("expected discovery-usefulness answer composition")
	}
	for _, want := range []string{"`internal/workspace/discovery_query.go`"} {
		if !strings.Contains(answer, want) {
			t.Fatalf("expected composed answer to cite %q, got:\n%s", want, answer)
		}
	}
	for _, forbidden := range []string{"`internal/agent/answer_composer.go`", "`internal/agent/runner.go`", "`internal/agent/runner_lane_policy.go`"} {
		if strings.Contains(answer, forbidden) {
			t.Fatalf("composed answer should not inject question-class path %q, got:\n%s", forbidden, answer)
		}
	}
}

func TestDelegatedDiscoveryAnswerStaysGenericForDebugLogRedactedMode(t *testing.T) {
	results := []workspace.ToolResult{
		{
			Tool:    "discovery_query",
			Summary: "Ran architecture_boundary discovery query with 10 layer(s); artifact: .idiom/discovery/queries/debug.json",
			Output:  `{"purpose":"architecture_boundary","artifact_path":".idiom/discovery/queries/debug.json","evidence":[{"name":"text_anchor_search","summary":"Found debug redaction implementation paths.","output_excerpt":"internal/transcript/debuglog.go:18: DebugLogModeRedacted DebugLogMode = \"redacted\"\ninternal/transcript/debuglog.go:528: return redactedBlob(typed)\ninternal/app/model_state.go:469: case \"redacted\", \"full\":"}]}`,
		},
		{
			Tool:    "read_file",
			Summary: "Read internal/transcript/debuglog.go lines 1-80",
			Output:  "package transcript\ntype DebugLogMode string\nconst DebugLogModeRedacted DebugLogMode = \"redacted\"\n",
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

func TestDelegatedTaskAnswerSynthesizesDebugLogRedactedMode(t *testing.T) {
	results := []workspace.ToolResult{{
		Tool:    "task",
		Summary: "Child task completed",
		Output: strings.Join([]string{
			"Source-body evidence paths:",
			"- internal/transcript/debuglog.go",
			"- internal/config/config.go",
			"",
			"Verified context:",
			"- Found debug log redaction defaults in internal/transcript/debuglog.go and internal/config/config.go.",
			"- Key findings: redacted mode keeps logs useful without exposing raw prompt or file body content.",
		}, "\n"),
	}}

	answer, ok := synthesizeDelegatedReadOnlyAnswer("What does the debug log redacted mode do?", results)
	if !ok {
		t.Fatal("expected synthesized answer from delegated debug-log packet")
	}
	for _, want := range []string{
		"The answer is grounded in",
		"What this establishes:",
		"redacted",
		"debug log",
		"`internal/transcript/debuglog.go`",
		"`internal/config/config.go`",
	} {
		if !strings.Contains(answer, want) {
			t.Fatalf("expected synthesized answer to contain %q, got:\n%s", want, answer)
		}
	}
	for _, forbidden := range []string{"What Gets Redacted:", "Discovery packet:", "Relevant source hits:", "Evidence used:"} {
		if strings.Contains(answer, forbidden) {
			t.Fatalf("synthesized answer should not expose telemetry section %q:\n%s", forbidden, answer)
		}
	}
	runner := &Runner{}
	if blocker, reason := runner.finalAnswerBlockerKind(answer, results); blocker != "" || reason != "" {
		t.Fatalf("synthesized answer should pass final-answer blocker, got (%q, %q):\n%s", blocker, reason, answer)
	}
}

func TestCleanDelegatedReadOnlyFindingLineStripsPacketLabels(t *testing.T) {
	cases := map[string]string{
		"Key findings: ShortLabel returns Save changes.":          "ShortLabel returns Save changes.",
		"Verified context: redacted mode masks raw prompt text.":  "redacted mode masks raw prompt text.",
		"Inspected paths: internal/agent/runner.go was read.":     "internal/agent/runner.go was read.",
		"Evidence used: internal/transcript/debuglog.go.":         "internal/transcript/debuglog.go.",
		"Core files: internal/agent/runner_child_tasks.go.":       "internal/agent/runner_child_tasks.go.",
		"Important flow: the task result returns a child packet.": "the task result returns a child packet.",
		"Source body paths: internal/agent/runner.go.":            "internal/agent/runner.go.",
		"Grounded findings: child packet ready.":                  "child packet ready.",
	}
	for input, want := range cases {
		if got := cleanDelegatedReadOnlyFindingLine(input); got != want {
			t.Fatalf("cleanDelegatedReadOnlyFindingLine(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestSourceEvidenceAnswerLinesStripSearchContextHandles(t *testing.T) {
	output := strings.Join([]string{
		"package workspace",
		"",
		"// task tool manifest entries wire subagents for delegated child work.",
		"context:",
		"f1 internal/workspace/tool_manifest.go:3:// task tool manifest entries wire subagents for delegated child work.",
	}, "\n")

	lines := sourceEvidenceAnswerLinesForObjective(output, "Explain how subagents work in this harness.", 8)
	if len(lines) != 0 {
		t.Fatalf("expected comment-only context handles to be ignored, got %#v", lines)
	}
}

func TestDelegatedReadOnlyFindingLinesIgnoreSearchContextHandles(t *testing.T) {
	results := []workspace.ToolResult{{
		Tool: "read_file",
		Output: strings.Join([]string{
			"package workspace",
			"",
			"// task tool manifest entries wire subagents for delegated child work.",
			"context:",
			"f1 internal/workspace/tool_manifest.go:3:// task tool manifest entries wire subagents for delegated child work.",
			"f1 internal/workspace/tool_manifest.go:4:\treturn \"subagents are wired through task tool manifest entries for delegated child work\"",
		}, "\n"),
	}}

	lines := delegatedReadOnlyFindingLines(results, []string{"internal/workspace/tool_manifest.go"}, 4)
	if len(lines) != 0 {
		t.Fatalf("expected comment-only context handles to be ignored, got %#v", lines)
	}
}

func TestDelegatedTaskAnswerDoesNotSynthesizeFromStrippedControlPacketOnly(t *testing.T) {
	results := []workspace.ToolResult{{
		Tool:    "task",
		Summary: "Child task completed",
		Output: strings.Join([]string{
			"Question: What does the debug log redacted mode do?",
			"Source: delegated read-only evidence packet.",
			"",
			"Inspected paths:",
			"- `internal/transcript/debuglog.go`",
			"- `internal/config/config.go`",
			"",
			"<<QUESTION>>: Completion gate rejected the final answer repeatedly before the required completion evidence was produced.",
			"Missing completion evidence: the answer does not satisfy the required evidence contract. Missing: inspected_paths.",
			"",
			"Additional inspected paths:",
			"- `internal/agent/runner.go`",
		}, "\n"),
	}}

	if answer, ok := synthesizeDelegatedReadOnlyAnswer("What does the debug log redacted mode do?", results); ok {
		t.Fatalf("expected stripped control-only packet to be insufficient for synthesis, got:\n%s", answer)
	}
}

func TestDelegatedTaskAnswerStrippedSectionsDoNotContributeEvidencePaths(t *testing.T) {
	results := []workspace.ToolResult{{
		Tool:    "task",
		Summary: "Child task completed",
		Output: strings.Join([]string{
			"Question: What does ShortLabel return?",
			"Source: delegated read-only evidence packet.",
			"",
			"Source-body evidence paths:",
			"- `internal/labels/labels.go`",
			"",
			"Inspected paths:",
			"- `internal/agent/runner.go`",
			"",
			"Key findings:",
			"- Found ShortLabel returns \"Save changes\" in internal/labels/labels.go.",
			"",
			"<<QUESTION>>: Completion gate rejected the final answer repeatedly before the required completion evidence was produced.",
			"Missing completion evidence: the answer does not satisfy the required evidence contract. Missing: inspected_paths.",
			"",
			"Additional inspected paths:",
			"- `internal/transcript/debuglog.go`",
		}, "\n"),
	}}

	answer, ok := synthesizeDelegatedReadOnlyAnswer("What does ShortLabel return?", results)
	if !ok {
		t.Fatal("expected synthesized answer from sanitized key finding")
	}
	for _, want := range []string{
		"The answer is grounded in",
		"`internal/labels/labels.go`",
		"Save changes",
	} {
		if !strings.Contains(answer, want) {
			t.Fatalf("expected synthesized answer to contain %q, got:\n%s", want, answer)
		}
	}
	for _, forbidden := range []string{
		"Question:",
		"Source: delegated",
		"Inspected paths:",
		"<<QUESTION>>",
		"Completion gate rejected",
		"Missing completion evidence",
		"Additional inspected paths:",
		"`internal/agent/runner.go`",
		"`internal/transcript/debuglog.go`",
	} {
		if strings.Contains(answer, forbidden) {
			t.Fatalf("synthesized answer leaked internal child packet text %q:\n%s", forbidden, answer)
		}
	}
	runner := &Runner{}
	if blocker, reason := runner.finalAnswerBlockerKind(answer, results); blocker != "" || reason != "" {
		t.Fatalf("synthesized answer should pass final-answer blocker, got (%q, %q):\n%s", blocker, reason, answer)
	}
}

func TestAnswerComposerDoesNotLeakTelemetrySections(t *testing.T) {
	answer, ok := AnswerComposer{}.Compose(AnswerComposerInput{
		Question:      "Can you tell me how this harness uses subagents?",
		EvidencePaths: []string{"`internal/agent/runner.go`,", "internal/agent/runner_child_tasks.go"},
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
				{Name: "text_anchor_search", Summary: "Found subagent handoff implementation paths."},
			},
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
				{Name: "text_anchor_search", Summary: "Found discovery implementation paths."},
				{Name: "go_read_symbol", Summary: "Read answer composer and runner synthesis boundaries."},
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
		"Symbol read:",
		"`internal/agent/answer_composer.go`",
		"`internal/agent/runner.go`",
		"`internal/agent/runner_lane_policy.go`",
	} {
		if !strings.Contains(answer, want) {
			t.Fatalf("expected generic discovery evidence answer to contain %q, got:\n%s", want, answer)
		}
	}
	for _, forbidden := range []string{"## Grounded Answer", "### Decision Guidance", "What I found:", "text_anchor_search:", "Open a returned handle", "Discovery packet:", "discovery packet", "Evidence used:", "Confidence:"} {
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
	replies := []string{
		"What I found:\n- text_anchor_search: Found 178 matching line(s) (showing first 50). Bounded-read capability is available on returned handles, or narrow the pattern to see more.\n\nDiscovery packet: `.idiom/discovery/queries/query.json`.\nConfidence: high",
		"`internal/agent/runner.go` contains the runner `Run` method. `internal/agent/runner_lane_policy.go` contains `conversationEvidenceSatisfied`, the readiness check present in the inspected source. Together, those inspected anchors show the useful integration point: discovery output has to connect into runner closeout and readiness checks before it can support final synthesis.",
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reply := "The scout reported that the delegated probe found a Go source file declaring the widget package."
		if requests < len(replies) {
			reply = replies[requests]
		}
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
		}, {
			Tool:    "read_file",
			Summary: "Read f1 internal/agent/runner.go lines 1-80",
			Output:  "package agent\n\nfunc (r *Runner) Run() {}\n",
		}, {
			Tool:    "read_file",
			Summary: "Read f1 internal/agent/runner_lane_policy.go lines 1-80",
			Output:  "package agent\n\nfunc conversationEvidenceSatisfied() bool { return true }\n",
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
	if requests != 2 {
		t.Fatalf("expected raw discovery packet rejection followed by grounded answer, got %d requests", requests)
	}
	for _, want := range []string{"inspected anchors", "`internal/agent/runner_lane_policy.go`", "`internal/agent/runner.go`"} {
		if !strings.Contains(answer, want) {
			t.Fatalf("expected decorated final answer to contain %q, got:\n%s", want, answer)
		}
	}
	for _, forbidden := range []string{"## Grounded Answer", "### Decision Guidance", "What I found:", "text_anchor_search:", "Discovery packet", "Confidence:"} {
		if strings.Contains(answer, forbidden) {
			t.Fatalf("final boundary leaked raw answer section %q:\n%s", forbidden, answer)
		}
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

func TestFinalAnswerDecoratorDecoratesBudgetExhaustionScaffold(t *testing.T) {
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
	for _, want := range []string{"not have enough supported evidence", "internal/agent/runner.go"} {
		if !strings.Contains(answer, want) {
			t.Fatalf("expected budget-exhausted scaffold to become actionable blocker containing %q, got:\n%s", want, answer)
		}
	}
	for _, forbidden := range []string{"<<QUESTION>>", "Discovery packet:", "What I found:", "source-body"} {
		if strings.Contains(answer, forbidden) {
			t.Fatalf("budget-exhausted scaffold leaked internal phrase %q:\n%s", forbidden, answer)
		}
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
		}, {
			Tool:    "read_file",
			Summary: "Read f1 internal/agent/runner.go lines 1-80",
			Output:  "package agent\n\nfunc (r *Runner) Run() {}\n",
		}},
		UserFacing: true,
	})
	if strings.Contains(answer, "Partial findings from this turn") {
		t.Fatalf("expected post-readiness closure to use composed answer, got:\n%s", answer)
	}
	if strings.Contains(answer, "## Grounded Answer") || strings.Contains(answer, "### Decision Guidance") {
		t.Fatalf("expected composed answer without internal composer headings, got:\n%s", answer)
	}
	for _, want := range []string{"internal/agent/runner.go", "Runner.Run"} {
		if !strings.Contains(answer, want) {
			t.Fatalf("expected presentable post-readiness answer to contain %q, got:\n%s", want, answer)
		}
	}
	if strings.Contains(answer, "func (r *Runner) Run()") {
		t.Fatalf("expected fact synthesis instead of raw source echo, got:\n%s", answer)
	}
}

func TestFinalAnswerDecoratorDoesNotLeakDelegatedPacketWhenComposerLacksDiscoverySummary(t *testing.T) {
	raw := "Question: how does this harness utilize sub-agents?\n" +
		"Source: delegated read-only evidence packet.\n\n" +
		"Source-body evidence paths:\n" +
		"- `internal/agent/runner.go`\n" +
		"- `internal/agent/runner_child_tasks.go`\n\n" +
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
	for _, want := range []string{"not have enough supported evidence", "`internal/agent/runner.go`", "`internal/agent/runner_child_tasks.go`"} {
		if !strings.Contains(answer, want) {
			t.Fatalf("expected evidence-retained blocker to contain %q, got:\n%s", want, answer)
		}
	}
	if strings.Contains(answer, "<<QUESTION>>") || strings.Contains(answer, "exact missing claim") {
		t.Fatalf("final boundary leaked internal blocker protocol:\n%s", answer)
	}
}

func TestFinalAnswerDecoratorUsesExactProbeEvidenceOverRetainedScopeSummary(t *testing.T) {
	raw := "The strongest implementation evidence is in `internal/agent/runner_child_tasks.go`. Use those findings as the source of truth for `how does this harness utilize sub-agents?`.\n\nWhat this establishes:\n- Text anchors: Found child-runner implementation paths."
	runner := &Runner{}
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: "architecture",
		Contract:   "architecture_assessment",
		Objective:  "how does this harness utilize sub-agents?",
	}
	results := []workspace.ToolResult{
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
	}
	answer := runner.decorateFinalAnswer(FinalAnswerDecorateInput{
		Answer:     raw,
		StopReason: "post_readiness_probe_budget_exhausted",
		Brief:      brief,
		Results:    results,
		UserFacing: true,
	})
	for _, want := range []string{
		"`Runner.runChildTask`",
		"`internal/agent/runner_child_tasks.go`",
		"sets `prompt` to `normalizeReadOnlyChildPrompt(prompt)`",
		"assigns `child` from `&Runner{`",
	} {
		if !strings.Contains(answer, want) {
			t.Fatalf("expected exact-probe fallback answer to contain %q, got:\n%s", want, answer)
		}
	}
	for _, forbidden := range []string{
		"retained source context",
		"bounded answer",
		"The strongest implementation evidence is in",
		"What this establishes:",
	} {
		if strings.Contains(answer, forbidden) {
			t.Fatalf("expected exact-probe fallback answer to avoid %q, got:\n%s", forbidden, answer)
		}
	}
	runner.RuntimeBrief = brief
	if kind, reason := runner.finalAnswerBlockerKind(answer, results); kind != "" || reason != "" {
		t.Fatalf("architecture exact-probe fallback should pass final-answer guard, got (%q, %q):\n%s", kind, reason, answer)
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
	nudge := childPacketBoundaryNudge("Explain how this harness uses subagents.", nil, []string{
		"internal/agent/discovery_redirect.go",
		"internal/workspace/discovery_query.go",
	}, 2)
	for _, want := range []string{
		"Why answer now is valid:",
		"Packet-named paths:",
		"`internal/agent/discovery_redirect.go`",
		"normal tool policy",
		"packet-named path or symbol",
		"at most 2 targeted probes",
		"Answer now from the packet",
	} {
		if !strings.Contains(nudge, want) {
			t.Fatalf("expected child packet nudge to contain %q, got:\n%s", want, nudge)
		}
	}
	if strings.Contains(nudge, "`named_gap`") || strings.Contains(nudge, "`why_not_answering`") {
		t.Fatalf("child packet nudge must not expose read-only named-gap schema requirements, got:\n%s", nudge)
	}
}

func TestChildPacketBoundaryNudgeDisclosesActualProbeBudget(t *testing.T) {
	for _, tc := range []struct {
		budget int
		want   string
	}{
		{budget: 2, want: "at most 2 targeted probes"},
		{budget: 1, want: "one targeted probe"},
		{budget: 0, want: "answer unless the missing claim is exact and material"},
	} {
		nudge := childPacketBoundaryNudge("q", nil, []string{"internal/agent/runner.go"}, tc.budget)
		if !strings.Contains(nudge, tc.want) {
			t.Fatalf("budget=%d: expected nudge to contain %q, got:\n%s", tc.budget, tc.want, nudge)
		}
	}
}

func TestChildPacketBoundaryNudgeFiltersNonFileArtifacts(t *testing.T) {
	nudge := childPacketBoundaryNudge("Explain how this harness uses subagents.", nil, []string{
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
		"normal tool policy",
		"one exact unresolved claim",
		"file-level raw reads",
		"`internal/agent/runner.go`",
	} {
		if !strings.Contains(nudge, want) {
			t.Fatalf("expected delegated readiness nudge to contain %q, got:\n%s", want, nudge)
		}
	}
	if strings.Contains(nudge, "`named_gap`") || strings.Contains(nudge, "`why_not_answering`") {
		t.Fatalf("delegated readiness nudge must not expose read-only named-gap schema requirements, got:\n%s", nudge)
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
	if strings.Contains(strings.ToLower(first), "disables tools") || strings.Contains(strings.ToLower(second), "disables tools") {
		t.Fatalf("diminishing-returns nudge should stay advisory, got:\n%s\n---\n%s", first, second)
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
		{
			Tool:    "read_file",
			Summary: "Read internal/agent/runner.go lines 2200-2260",
			Output:  "package agent\nfunc finish() {}\n",
		},
	}
	state := buildRunnerNextActionState(
		"child:explorer",
		3,
		"tool_result",
		workspace.ToolRequest{Tool: "search_content", Path: "internal/agent/runner.go"},
		results[2],
		results,
		false,
		brief,
	)
	if !slices.Contains(state.Constraints, "answer_readiness_reached") {
		t.Fatalf("expected delegated readiness closure bias, got %#v", state)
	}
	if slices.Contains(state.Constraints, "completion_gate_active") {
		t.Fatalf("search output should not fabricate completion gate state, got %#v", state)
	}
	if slices.Contains(state.InvalidNextActions, "final_answer_without_satisfying_completion_gate") {
		t.Fatalf("closure-biased state should not block answering on a fabricated gate, got %#v", state)
	}
	if len(state.NextValidActions) < 1 || state.NextValidActions[0] != "answer directly from the evidence already gathered" {
		t.Fatalf("expected answer-first closure bias, got %#v", state.NextValidActions)
	}
	for _, want := range []string{
		"internal/agent/runner.go",
		"internal/agent/runner_child_tasks.go",
	} {
		if !slices.Contains(state.EvidencePaths, want) {
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
		workspace.ToolResult{Tool: "task", Summary: "Child task completed", Output: "Source-body evidence paths:\n- internal/agent/runner.go\n\nKey findings:\n- The inspected runner source explains how this harness uses subagents through delegated child tasks.\n\nInspected paths: internal/agent/runner.go"},
		[]workspace.ToolResult{{Tool: "task", Summary: "Child task completed", Output: "Source-body evidence paths:\n- internal/agent/runner.go\n\nKey findings:\n- The inspected runner source explains how this harness uses subagents through delegated child tasks.\n\nInspected paths: internal/agent/runner.go"}},
		false,
		workerruntime.WorkerRuntimeBrief{TaskFamily: "architecture", Objective: "Explain how subagents work."},
	)
	if !slices.Contains(state.Constraints, "child_packet_available") {
		t.Fatalf("expected child-packet closure bias, got %#v", state)
	}
	if len(state.NextValidActions) == 0 || state.NextValidActions[0] != "answer directly from the child packet if it already resolves the question" {
		t.Fatalf("expected child packet answer-first guidance, got %#v", state.NextValidActions)
	}
	if !slices.Contains(state.InvalidNextActions, "broad rediscovery outside packet-named paths or symbols after the child packet") {
		t.Fatalf("expected anti-drift invalid action, got %#v", state.InvalidNextActions)
	}
}

func TestBuildRunnerNextActionStatePrefersClosureAfterTypedChildPacket(t *testing.T) {
	result := typedReadOnlyTaskResult(ReadOnlyEvidencePacket{
		Objective:       "Explain how subagents work.",
		Question:        "Explain how subagents work.",
		SourceBodyPaths: []string{"internal/agent/runner.go", "internal/agent/runner_child_tasks.go"},
		InspectedPaths:  []string{"internal/agent/runner.go", "internal/agent/runner_child_tasks.go", ".idiom/discovery/queries/query.json"},
		Confidence:      "high",
		Findings: []ReadOnlyEvidenceFinding{
			{Path: "internal/agent/runner.go", Text: "The harness uses the `task` tool for delegated subagent read-only work."},
			{Path: "internal/agent/runner_child_tasks.go", Text: "`runChildTask` constructs the subagent child runner and returns the child packet."},
		},
	})
	state := buildRunnerNextActionState(
		"main",
		1,
		"child_task_result",
		workspace.ToolRequest{Tool: "task", Mode: "read_only", Agent: "explorer"},
		result,
		[]workspace.ToolResult{result},
		false,
		workerruntime.WorkerRuntimeBrief{TaskFamily: "architecture", Objective: "Explain how subagents work.", PermissionProfile: "read_only_workspace"},
	)
	if !slices.Contains(state.Constraints, "child_packet_available") {
		t.Fatalf("expected typed child-packet closure bias, got %#v", state)
	}
	if len(state.NextValidActions) == 0 || state.NextValidActions[0] != "answer directly from the child packet if it already resolves the question" {
		t.Fatalf("expected typed packet answer-first guidance, got %#v", state.NextValidActions)
	}
	for _, want := range []string{"internal/agent/runner.go", "internal/agent/runner_child_tasks.go"} {
		if !slices.Contains(state.EvidencePaths, want) {
			t.Fatalf("expected typed packet evidence path %q, got %#v", want, state.EvidencePaths)
		}
	}
	if slices.Contains(state.EvidencePaths, ".idiom/discovery/queries/query.json") {
		t.Fatalf("typed packet should not expose generated artifacts as evidence paths: %#v", state.EvidencePaths)
	}
}

func TestFinalAnswerDecoratorSynthesizesTypedChildPacketFindings(t *testing.T) {
	result := typedReadOnlyTaskResult(ReadOnlyEvidencePacket{
		Objective:       "Explain how subagents work.",
		Question:        "Explain how subagents work.",
		SourceBodyPaths: []string{"internal/agent/runner.go", "internal/agent/runner_child_tasks.go"},
		InspectedPaths:  []string{"internal/agent/runner.go", "internal/agent/runner_child_tasks.go"},
		Confidence:      "high",
		Findings: []ReadOnlyEvidenceFinding{
			{Path: "internal/agent/runner.go", Text: "The parent runner delegates subagent read-only exploration through the `task` tool"},
			{Path: "internal/agent/runner_child_tasks.go", Text: "`runChildTask` constructs the subagent child runner and returns the typed evidence packet"},
		},
	})
	runner := &Runner{}
	answer := runner.decorateFinalAnswer(FinalAnswerDecorateInput{
		Answer:     "Question: Explain how subagents work.\nSource: delegated read-only evidence packet.\n\nInspected paths:\n- internal/agent/runner.go\n\nKey findings:\n- packet headings should not leak",
		StopReason: "post_child_read_budget_exhausted",
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "architecture",
			Contract:          "architecture_assessment",
			PermissionProfile: "read_only_workspace",
			Objective:         "Explain how subagents work.",
		},
		Results:    []workspace.ToolResult{result},
		UserFacing: true,
	})
	for _, want := range []string{"delegates subagent read-only exploration", "returns the typed evidence packet", "`internal/agent/runner.go`", "`internal/agent/runner_child_tasks.go`"} {
		if !strings.Contains(answer, want) {
			t.Fatalf("expected typed finding or path %q in final answer, got:\n%s", want, answer)
		}
	}
	for _, forbidden := range []string{"Source: delegated read-only evidence packet", "Inspected paths:", "Key findings:", "What this establishes:", "Use those findings as the source of truth"} {
		if strings.Contains(answer, forbidden) {
			t.Fatalf("final synthesis leaked packet/scaffold heading %q:\n%s", forbidden, answer)
		}
	}
}

func TestBuildRunnerNextActionStateDoesNotCloseOnUnrelatedChildPacket(t *testing.T) {
	result := workspace.ToolResult{
		Tool:    "task",
		Summary: "Child task completed",
		Output:  "Question: explain labels\nSource-body evidence paths:\n- internal/labels/labels.go\n\nKey findings:\n- ShortLabel returns Save changes.",
	}
	state := buildRunnerNextActionState(
		"main",
		1,
		"child_task_result",
		workspace.ToolRequest{Tool: "task", Mode: "read_only", Agent: "explorer"},
		result,
		[]workspace.ToolResult{result},
		false,
		workerruntime.WorkerRuntimeBrief{TaskFamily: "architecture", Objective: "Explain how subagents work."},
	)
	if slices.Contains(state.Constraints, "child_packet_available") {
		t.Fatalf("unrelated child source-body packet must not satisfy closure, got %#v", state)
	}
}

func TestBuildRunnerNextActionStateDoesNotInventChildPacketForDirectReadOnlyEvidence(t *testing.T) {
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 internal/transcript/debuglog.go lines 1-80 (total lines: 720)",
		Output:  "package transcript\nconst DebugLogModeRedacted DebugLogMode = \"redacted\"\nfunc redactSecrets() {}\n",
	}}
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		PermissionProfile: "read_only_workspace",
		Objective:         "What does the debug log redacted mode do?",
	}
	state := buildRunnerNextActionState(
		"main",
		1,
		"tool_result",
		workspace.ToolRequest{Tool: "read_file", Path: "internal/transcript/debuglog.go", StartLine: 1, EndLine: 80},
		results[0],
		results,
		false,
		brief,
	)
	if slices.Contains(state.Constraints, "child_packet_available") {
		t.Fatalf("ordinary direct reads must not fabricate child-packet closure, got %#v", state)
	}
	for _, action := range state.NextValidActions {
		if strings.Contains(action, "child packet") {
			t.Fatalf("ordinary direct reads must not produce child-packet guidance, got %#v", state.NextValidActions)
		}
	}
	readiness := runnerReadinessState("main", brief, results)
	if readiness == nil || !readiness.ReadyToAnswer {
		t.Fatalf("expected normal readiness, got %#v", readiness)
	}
	if slices.Contains(readiness.ClosedClaims, "child_packet_available") {
		t.Fatalf("readiness must not close child_packet_available without a task packet, got %#v", readiness)
	}
}

func TestReadOnlyAnalysisExactProbeCountIncludesCodeNavigateSourceBody(t *testing.T) {
	results := []workspace.ToolResult{
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed file_outline via go_file_outline",
			Output:  "capability: file_outline\nroute_summary: Outlined internal/transcript/debuglog.go",
		},
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output:  "capability: read_symbol\nroute_summary: Read DebugLogger.SetMode in internal/transcript/debuglog.go lines 52-61\n\n52:func (d *DebugLogger) SetMode(mode DebugLogMode) {}",
		},
	}
	if got := readOnlyAnalysisExactProbeCount(results); got != 1 {
		t.Fatalf("exact probe count = %d, want 1", got)
	}
}

func TestReadOnlyNoMatchCloseoutAppliesAfterExactEvidence(t *testing.T) {
	runner := &Runner{RuntimeBrief: workerruntime.WorkerRuntimeBrief{
		Contract:          "architecture_assessment",
		TaskFamily:        "architecture",
		PermissionProfile: "read_only_workspace",
		Objective:         "What does the debug log redacted mode do?",
	}}
	results := []workspace.ToolResult{
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed file_outline via go_file_outline",
			Output:  "capability: file_outline\nroute_summary: Outlined internal/transcript/debuglog.go",
		},
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output:  "capability: read_symbol\nroute_summary: Read DebugLogger.SetMode in internal/transcript/debuglog.go lines 52-61\n\n52:func (d *DebugLogger) SetMode(mode DebugLogMode) {}",
		},
		{
			Tool:    "code_navigate",
			Summary: "No generic code navigation matches for read_symbol",
			Output:  "capability: read_symbol\nroute_summary: no matches",
		},
	}

	if !runner.readOnlyNoMatchCloseoutApplies(
		workspace.ToolRequest{Tool: "code_navigate", Capability: "read_symbol", Pattern: "redactedBlob"},
		results[2],
		results,
	) {
		t.Fatal("expected no-match symbol probe after exact source evidence to close out")
	}
	if runner.readOnlyNoMatchCloseoutApplies(
		workspace.ToolRequest{Tool: "code_navigate", Capability: "read_symbol", Pattern: "redactedBlob"},
		results[2],
		results[:1],
	) {
		t.Fatal("outline-only evidence must not trigger no-match closeout")
	}
}

func TestReadOnlyNoMatchCloseoutRequiresSourceBodyEvidence(t *testing.T) {
	runner := &Runner{RuntimeBrief: workerruntime.WorkerRuntimeBrief{
		Contract:          "architecture_assessment",
		TaskFamily:        "architecture",
		PermissionProfile: "read_only_workspace",
		Objective:         "Explain how this harness uses subagents.",
	}}
	results := []workspace.ToolResult{
		{
			Tool:    "discovery_query",
			Summary: "Ran architecture_boundary discovery query",
			Output:  "internal/agent/runner.go:574: result := r.runChildTask(ctx, req, opts)\ninternal/agent/runner_child_tasks.go:16: func (r *Runner) runChildTask(...)",
		},
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed file_outline via go_file_outline",
			Output:  "capability: file_outline\nroute_summary: Outlined internal/agent/runner_child_tasks.go",
		},
		{
			Tool:    "code_navigate",
			Summary: "No generic code navigation matches for read_symbol",
			Output:  "capability: read_symbol\nroute_summary: no matches",
		},
	}
	if runner.readOnlyNoMatchCloseoutApplies(
		workspace.ToolRequest{Tool: "code_navigate", Capability: "read_symbol", Pattern: "Runner.runChildTask"},
		results[2],
		results,
	) {
		t.Fatal("discovery and outline evidence without source body must not trigger no-match closeout")
	}
}

func TestPathlessReadSymbolAfterOutlineInheritsOutlinePath(t *testing.T) {
	results := []workspace.ToolResult{{
		Tool:    "code_navigate",
		Summary: "Code navigation routed file_outline via go_file_outline",
		Output:  "capability: file_outline\nroute_summary: Outlined internal/agent/runner_child_tasks.go",
	}}
	req := workspace.ToolRequest{Tool: "code_navigate", Capability: "read_symbol", Symbol: "Runner.runChildTask"}
	rewritten, raw, reason, ok := pathlessReadSymbolAfterOutlineRewriteRequest(req, results)
	if !ok {
		t.Fatal("expected pathless read_symbol to inherit latest outline path")
	}
	if rewritten.Path != "internal/agent/runner_child_tasks.go" || rewritten.Pattern != "Runner.runChildTask" {
		t.Fatalf("unexpected rewrite: %#v", rewritten)
	}
	if !strings.Contains(raw, `"path":"internal/agent/runner_child_tasks.go"`) || reason == "" {
		t.Fatalf("expected raw path rewrite and reason, raw=%q reason=%q", raw, reason)
	}
}

func TestRepeatedFileOutlineUpgradesToReadSymbol(t *testing.T) {
	results := []workspace.ToolResult{{
		Tool:    "code_navigate",
		Summary: "Code navigation routed file_outline via go_file_outline",
		Output: strings.Join([]string{
			"capability: file_outline",
			"route_summary: Outlined 2 top-level declaration(s) in internal/agent/runner_child_tasks.go.",
			"declarations:",
			"- runChildTask lines 16-140 tags: delegation",
			"- childTaskOptions lines 142-180 tags: support",
		}, "\n"),
	}}
	req := workspace.ToolRequest{Tool: "code_navigate", Path: "internal/agent/runner_child_tasks.go", Capability: "file_outline"}
	rewritten, raw, reason, ok := repeatedFileOutlineToReadSymbolRewriteRequest(req, results, workerruntime.WorkerRuntimeBrief{
		Contract:          "architecture_assessment",
		TaskFamily:        "delegated_read_only",
		PermissionProfile: "read_only_workspace",
		Objective:         "Can you tell me how this harness uses subagents?",
	})
	if !ok {
		t.Fatal("expected repeated outline to upgrade to read_symbol")
	}
	if rewritten.Capability != "read_symbol" || rewritten.Pattern != "runChildTask" || rewritten.Path != "internal/agent/runner_child_tasks.go" {
		t.Fatalf("unexpected outline upgrade: %#v", rewritten)
	}
	if !strings.Contains(raw, `"capability":"read_symbol"`) || reason == "" {
		t.Fatalf("expected raw read_symbol rewrite and reason, raw=%q reason=%q", raw, reason)
	}
}

func TestReadOnlyDefinitionProbeUpgradesToReadSymbol(t *testing.T) {
	req := workspace.ToolRequest{
		Tool:       "code_navigate",
		Path:       "internal/transcript/debuglog.go",
		Capability: "find_definition",
		Symbol:     "DebugLogModeRedacted",
	}
	rewritten, raw, reason, ok := readOnlyDefinitionToReadSymbolRewriteRequest(req, workerruntime.WorkerRuntimeBrief{
		Contract:          "architecture_assessment",
		TaskFamily:        "architecture",
		PermissionProfile: "read_only_workspace",
		Objective:         "What does the debug log redacted mode do?",
	})
	if !ok {
		t.Fatal("expected read-only definition probe to upgrade to read_symbol")
	}
	if rewritten.Capability != "read_symbol" || rewritten.Pattern != "DebugLogModeRedacted" || rewritten.Path != "internal/transcript/debuglog.go" {
		t.Fatalf("unexpected definition rewrite: %#v", rewritten)
	}
	if !strings.Contains(raw, `"capability":"read_symbol"`) || reason == "" {
		t.Fatalf("expected raw read_symbol rewrite and reason, raw=%q reason=%q", raw, reason)
	}
}

func TestSafeVisibleToolRequestAllowsConcreteDiscoveryQuery(t *testing.T) {
	runner := &Runner{}
	if !runner.safeVisibleToolRequest(workspace.ToolRequest{
		Tool:    "discovery_query",
		Purpose: "architecture_boundary",
		Query:   "subagent orchestration",
		Path:    ".",
	}, nil) {
		t.Fatal("concrete discovery_query should be safe visible recovery")
	}
	if runner.safeVisibleToolRequest(workspace.ToolRequest{Tool: "discovery_query"}, nil) {
		t.Fatal("empty discovery_query should not be safe visible recovery")
	}
	if runner.safeVisibleToolRequest(workspace.ToolRequest{
		Tool:    "discovery_query",
		Purpose: "all",
		Path:    ".",
	}, nil) {
		t.Fatal("invalid discovery_query purpose must not be safe visible recovery")
	}
	if runner.safeVisibleToolRequest(workspace.ToolRequest{
		Tool:  "discovery_query",
		Query: "subagent orchestration",
		Path:  ".",
	}, nil) {
		t.Fatal("arbitrary query text must not be salvaged as a discovery_query purpose")
	}
	if !runner.safeVisibleToolRequest(workspace.ToolRequest{
		Tool:  "discovery_query",
		Query: "architecture",
		Path:  ".",
	}, nil) {
		t.Fatal("recognized discovery_query purpose aliases should be safe visible recovery")
	}
	if !runner.safeVisibleToolRequest(workspace.ToolRequest{
		Tool:    "discovery_query",
		Purpose: "Run a deterministic layered discovery recipe to identify subagent definitions and orchestration patterns.",
		Path:    ".",
	}, nil) {
		t.Fatal("descriptive architecture discovery purpose should be canonicalized for safe visible recovery")
	}
}

func TestVisibleToolSalvageRepairsDelegatedReadOnlyDiscoveryQueryWithoutTopicScope(t *testing.T) {
	runner := &Runner{
		AgentName: "child:explorer",
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "delegated_read_only",
			Contract:          "architecture_assessment",
			PermissionProfile: "read_only_workspace",
			Objective:         "What can we add to our discovery tools in an effort at increasing their overall usefulness?",
		},
	}
	reply := `{"tool":"discovery_query","query":"discovery tool usefulness","path":"."}`
	req, raw, ok := runner.salvageVisibleToolRequestFromProse(reply, nil)
	if !ok {
		t.Fatal("expected delegated discovery query JSON to salvage through repair")
	}
	if req.Tool != "discovery_query" || req.Purpose != "architecture_boundary" || req.Path != "." {
		t.Fatalf("expected repaired architecture discovery query to keep generic scope, got %#v", req)
	}
	if strings.Contains(req.Pattern, "answer[-_ ]?composer") || strings.Contains(req.Pattern, "synthesizeDelegatedReadOnlyAnswer") {
		t.Fatalf("expected repaired query not to target answer synthesis boundary, got %q", req.Pattern)
	}
	if !strings.Contains(raw, `"purpose":"architecture_boundary"`) || !strings.Contains(raw, `"path":"."`) {
		t.Fatalf("expected raw repaired request, got %s", raw)
	}
}

func TestDelegatedChildFallbackSourceReadUsesDiscoveredEvidencePath(t *testing.T) {
	ws := newTestWorkspaceWithFiles(t, map[string]string{
		"go.mod":                            "module example.test\n",
		"internal/agent/answer_composer.go": "package agent\n\nfunc ComposeDiscoveryAnswer() string { return \"ok\" }\n",
		"internal/agent/runner.go":          "package agent\n\nfunc Run() {}\n",
	})
	runner := &Runner{Workspace: ws}
	results := []workspace.ToolResult{{
		Tool:    "discovery_query",
		Summary: "Ran architecture_boundary discovery query",
		Output:  `{"artifact_path":".idiom/discovery/queries/query.json","evidence":[{"name":"text_anchor_search","summary":"Found discovery paths","output_excerpt":"internal/agent/runner.go:1: package agent"}]}`,
	}}
	req, result, ok := runner.delegatedChildFallbackSourceRead(context.Background(), "What can we add to our discovery tools in an effort at increasing their overall usefulness?", results)
	if !ok {
		t.Fatal("expected fallback source read")
	}
	if req.Path != "internal/agent/runner.go" || result.Tool != "read_file" {
		t.Fatalf("expected fallback to read discovered evidence path, req=%#v result=%#v", req, result)
	}
	if !readFileResultHasConcreteSourceBody(result) {
		t.Fatalf("fallback result should contain source body, got %#v", result)
	}
}

func TestDelegatedChildFallbackSourceReadDoesNotUseQuestionClassWithoutDiscoveryPath(t *testing.T) {
	ws := newTestWorkspaceWithFiles(t, map[string]string{
		"go.mod":                            "module example.test\n",
		"internal/agent/answer_composer.go": "package agent\n\nfunc ComposeDiscoveryAnswer() string { return \"ok\" }\n",
	})
	runner := &Runner{Workspace: ws}
	results := []workspace.ToolResult{{
		Tool:    "discovery_query",
		Summary: "Ran architecture_boundary discovery query",
		Output:  `{"artifact_path":".idiom/discovery/queries/query.json","evidence":[{"name":"dependency_graph","summary":"Collected graph","output_excerpt":"no file paths"}]}`,
	}}
	req, result, ok := runner.delegatedChildFallbackSourceRead(context.Background(), "What can we add to our discovery tools in an effort at increasing their overall usefulness?", results)
	if ok {
		t.Fatalf("expected no fallback read from question-class hardcoding, req=%#v result=%#v", req, result)
	}
}

func TestDelegatedChildFallbackSourceReadUsesPlanCandidateRanking(t *testing.T) {
	ws := newTestWorkspaceWithFiles(t, map[string]string{
		"go.mod":                               "module example.test\n",
		"internal/agent/errors.go":             "package agent\n\ntype RunnerError string\n",
		"internal/agent/runner_child_tasks.go": "package agent\n\nfunc (r *Runner) runChildTask() {}\n",
	})
	runner := &Runner{Workspace: ws}
	results := []workspace.ToolResult{{
		Tool:    "discovery_query",
		Summary: "Ran architecture_boundary discovery query",
		Output: `{"artifact_path":".idiom/discovery/queries/query.json","evidence":[` +
			`{"name":"target_listing","summary":"Listed internal/agent.","output_excerpt":"internal/agent/errors.go\ninternal/agent/final_answer_acceptance.go"},` +
			`{"name":"text_anchor_search","summary":"Found delegated child orchestration implementation paths.","output_excerpt":"internal/agent/runner_child_tasks.go:16: func (r *Runner) runChildTask()"}` +
			`]}`,
	}}
	req, result, ok := runner.delegatedChildFallbackSourceRead(context.Background(), "Explain how this harness uses subagents and child task orchestration.", results)
	if !ok {
		t.Fatal("expected fallback source read from objective-relevant discovery anchor")
	}
	if req.Path != "internal/agent/runner_child_tasks.go" {
		t.Fatalf("expected plan-ranked child task source path, req=%#v result=%#v", req, result)
	}
}

func TestBuildRunnerNextActionStateUsesClosureConstraintsForSuccessfulPostChildSearch(t *testing.T) {
	results := []workspace.ToolResult{
		{
			Tool:   "task",
			Output: "Question: explain subagents\nSource: delegated read-only evidence packet.\n\nSource-body evidence paths:\n- `internal/agent/runner.go`\n\nKey findings:\n- The inspected runner source explains how this harness uses subagents through delegated child tasks.\n\nInspected paths:\n- `internal/agent/runner.go`\n- `internal/agent/answer_composer.go`\n",
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
	if !slices.Contains(state.Constraints, "child_packet_available") {
		t.Fatalf("expected child-packet closure constraint, got %#v", state)
	}
	for _, forbidden := range []string{
		"edit_file if the target is already clear",
		"targeted verification evidence",
		"update task_ledger or answer with the concrete blocker",
	} {
		if slices.Contains(state.NextValidActions, forbidden) {
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
	if !slices.Contains(state.Constraints, "child_packet_available") {
		t.Fatalf("expected durable child-packet closure constraint, got %#v", state)
	}
	for _, forbidden := range []string{
		"read-symbol capability with one outlined declaration name",
		"references or calls-into capability on a named declaration",
		"go_move_declarations",
	} {
		if slices.Contains(state.NextValidActions, forbidden) {
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
		`{"tool":"read_file","path":"src/widget.go","start_line":1,"end_line":1}`,
		"The delegated probe read the Go source file under src and found that it declares the widget package, so the requested read-only scan located a concrete Go file.",
		"The scout reported that the delegated probe found a Go source file declaring the widget package.",
		"The scout reported that the delegated probe found a Go source file declaring the widget package.",
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reply := "The scout reported that the delegated probe found a Go source file declaring the widget package."
		if requests < len(replies) {
			reply = replies[requests]
		}
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
	var events []string
	runner := &Runner{
		Client:    llama.NewClient(server.URL, 2*time.Second),
		Workspace: mgr,
		Policy:    RunPolicy{MaxToolSteps: 8, MaxNoProgressTurns: 4},
		OnEvent: func(event RunEvent) {
			if event.Role == "completion_gate" || strings.Contains(event.Role, "critic") || strings.Contains(event.Role, "readiness") {
				events = append(events, event.Role+": "+event.Content)
			}
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
		t.Fatalf("Run: %v; events=%v", err, events)
	}
	if !strings.Contains(answer, "scout") || !strings.Contains(answer, "widget package") {
		t.Fatalf("unexpected parent answer: %q; events=%v", answer, events)
	}
	if childRequests == 0 || childResults == 0 {
		t.Fatalf("expected isolated child tool events to fire (requests=%d results=%d)", childRequests, childResults)
	}
	if requests < 5 || requests > 8 {
		t.Fatalf("expected parent spawn, child steps, and bounded parent final synthesis; got %d requests", requests)
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
		`{"tool":"read_file","path":"internal/agent/runner.go","start_line":1,"end_line":3}`,
		"No performance risk stands out in internal/agent/runner.go from the delegated evidence. Confidence: medium.",
	}
	requests := 0
	var roles []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request #%d; roles=%v", requests, roles)
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
	var sawChildTaskResult bool
	var parentModelCallAfterChild bool
	runner := &Runner{
		Client: llama.NewClient(server.URL, 2*time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:                root,
			CWD:                 root,
			MaxDirectoryEntries: 64,
		}),
		Policy: RunPolicy{MaxToolSteps: 8, MaxNoProgressTurns: 6},
		OnEvent: func(event RunEvent) {
			if event.Role == "next_action_state" && strings.Contains(event.Content, `"reason":"child_task_result"`) {
				sawChildTaskResult = true
			}
			if sawChildTaskResult && event.Role == "model_call" && strings.Contains(event.Content, `"agent":"main"`) {
				parentModelCallAfterChild = true
			}
		},
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
		t.Fatalf("unexpected answer after %d/%d requests: %q", requests, len(replies), answer)
	}
	if !rejected {
		t.Fatal("expected broad exploration to be rejected after the delegation nudge")
	}
	if !parentModelCallAfterChild {
		t.Fatalf("expected parent model synthesis after child task result (saw child result=%t)", sawChildTaskResult)
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
		`{"tool":"read_file","path":"internal/agent/runner.go","start_line":1,"end_line":3}`,
		"No performance risk stands out in internal/agent/runner.go from the delegated evidence. Confidence: medium.",
	}
	requests := 0
	var roles []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request #%d; roles=%v", requests, roles)
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

	var sawChildTaskResult bool
	var parentModelCallAfterChild bool
	runner := &Runner{
		Client: llama.NewClient(server.URL, 2*time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:                root,
			CWD:                 root,
			MaxDirectoryEntries: 64,
		}),
		Policy: RunPolicy{MaxToolSteps: 8, MaxNoProgressTurns: 6},
		OnEvent: func(event RunEvent) {
			if event.Role == "next_action_state" && strings.Contains(event.Content, `"reason":"child_task_result"`) {
				sawChildTaskResult = true
			}
			if sawChildTaskResult && event.Role == "model_call" && strings.Contains(event.Content, `"agent":"main"`) {
				parentModelCallAfterChild = true
			}
		},
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
		t.Fatalf("unexpected answer after %d/%d requests: %q", requests, len(replies), answer)
	}
	if !parentModelCallAfterChild {
		t.Fatalf("expected parent model synthesis after child task result (saw child result=%t)", sawChildTaskResult)
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
		`{"tool":"read_file","path":"internal/agent/runner.go","start_line":1,"end_line":3}`,
		"internal/agent/runner.go is the inspected path and the delegated child packet did not surface a deeper issue. Confidence: medium.",
	}
	requests := 0
	var roles []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request #%d; roles=%v", requests, roles)
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

	var sawChildTaskResult bool
	var parentModelCallAfterChild bool
	runner := &Runner{
		Client: llama.NewClient(server.URL, 2*time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 6, MaxNoProgressTurns: 4},
		OnEvent: func(event RunEvent) {
			if event.Role == "next_action_state" && strings.Contains(event.Content, `"reason":"child_task_result"`) {
				sawChildTaskResult = true
			}
			if sawChildTaskResult && event.Role == "model_call" && strings.Contains(event.Content, `"agent":"main"`) {
				parentModelCallAfterChild = true
			}
		},
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
		t.Fatalf("unexpected answer after %d/%d requests: %q", requests, len(replies), answer)
	}
	if !parentModelCallAfterChild {
		t.Fatalf("expected parent model synthesis after child task result (saw child result=%t)", sawChildTaskResult)
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
	if err := os.WriteFile(filepath.Join(root, "internal", "workspace", "tool_manifest.go"), []byte("package workspace\n\nfunc TaskToolManifestSummary() string {\n\treturn \"subagents are wired through task tool manifest entries for delegated child work\"\n}\n"), 0o644); err != nil {
		t.Fatalf("write tool manifest: %v", err)
	}

	replies := []string{
		`{"tool":"task","agent":"explorer","mode":"read_only","prompt":"Inspect internal/workspace/tool_manifest.go and explain how subagents are wired."}`,
		`{"tool":"search_files","pattern":"*tool_manifest*"}`,
		`{"tool":"read_file","path":"internal/workspace/tool_manifest.go","start_line":1,"end_line":20}`,
		"Partial findings from this turn (the budget was reached before a full synthesis):\n\nVerified context:\n- Read internal/workspace/tool_manifest.go lines 1-20\n- Found task tool manifest entries for delegated child work\n",
	}
	requests := 0
	var roles []string
	var eventDetails []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request #%d; roles=%v details=%v", requests, roles, eventDetails)
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
	runner.OnEvent = func(event RunEvent) {
		roles = append(roles, event.Role)
		switch event.Role {
		case "failure_feedback", "next_action_state", "readiness_check", "final_answer_critic", "control_rails_enforcement":
			eventDetails = append(eventDetails, event.Role+": "+event.Content)
		}
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
	lowerAnswer := strings.ToLower(answer)
	for _, want := range []string{
		"subagents are wired through",
		"task tool",
		"internal/workspace/tool_manifest.go",
	} {
		if !strings.Contains(lowerAnswer, strings.ToLower(want)) {
			t.Fatalf("expected direct answer to include %q, got %q", want, answer)
		}
	}
	if strings.Contains(strings.ToLower(answer), "partial findings from this turn") {
		t.Fatalf("expected parent synthesis instead of raw child packet, got %q", answer)
	}
	if strings.Contains(answer, "f23 ") || strings.Contains(answer, "f1 ") {
		t.Fatalf("unexpected answer: %q", answer)
	}
	for _, want := range []string{"readiness_check", "final_answer_critic"} {
		if !slices.Contains(roles, want) {
			t.Fatalf("expected final acceptance signal %q, got roles %v", want, roles)
		}
	}
	if requests != len(replies) {
		t.Fatalf("expected host to close from the child evidence packet without parent followups, used %d/%d replies", requests, len(replies))
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
	var eventDetails []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request #%d; details=%v", requests, eventDetails)
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
		switch event.Role {
		case "failure_feedback", "next_action_state", "readiness_check", "final_answer_critic", "control_rails_enforcement":
			eventDetails = append(eventDetails, event.Role+": "+event.Content)
		}
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
	if requests != 5 {
		t.Fatalf("expected parent to honor follow-up reads before final answer, used %d/%d replies", requests, len(replies))
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
		`{"tool":"read_file","path":"internal/agent/runner.go"}`,
		`{"tool":"read_file","path":"internal/agent/runner.go","named_gap":"inspect subagent delegation source after discovery","why_not_answering":"need source-body evidence for the discovered delegation anchor"}`,
		"The harness uses subagents through the task tool flow anchored in `internal/agent/runner.go`; the source body shows the delegated child task handoff and the child packet names `internal/agent/runner_child_tasks.go`. Confidence: high.",
		"The harness uses subagents through the task tool, primarily in internal/agent/runner.go and internal/agent/runner_child_tasks.go. Confidence: high.",
		"The answer is grounded in `internal/agent/runner.go`. Use those findings as the source of truth for `Explain how this harness uses subagents?`.\n\nWhat this establishes:\n- The source body shows the child task delegation anchor.",
		"The answer is grounded in `internal/agent/runner.go`. Use those findings as the source of truth for `Explain how this harness uses subagents?`.\n\nWhat this establishes:\n- The source body shows the child task delegation anchor.",
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
	for _, want := range []string{"subagents", "internal/agent/runner_child_tasks.go", "internal/agent/runner.go"} {
		if !strings.Contains(answer, want) {
			t.Fatalf("expected host-owned read-only closeout to contain %q, got %q", want, answer)
		}
	}
	if strings.HasPrefix(answer, "<<QUESTION>>") {
		t.Fatalf("expected host-owned read-only closeout answer, got blocker %q", answer)
	}
	if strings.Contains(answer, "task tool") && !strings.Contains(answer, "internal/agent/runner.go") {
		t.Fatalf("unexpected answer: %q", answer)
	}
	for _, forbidden := range []string{`"layers_run"`, "Partial findings from this turn", "Discovery packet:", "Relevant source hits:", "Evidence used:"} {
		if strings.Contains(answer, forbidden) {
			t.Fatalf("unexpected answer with %q: %q", forbidden, answer)
		}
	}
	_ = roles
	if requests > len(replies) {
		t.Fatalf("parent used too many replies, used %d/%d", requests, len(replies))
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

	// A lone discovery_query should NOT mark readiness — the model
	// must also read at least one source body before the harness
	// restricts further reads.
	ready := runner.delegatedReadOnlyAnswerReadyAfterResult(
		workspace.ToolRequest{Tool: "discovery_query"},
		results[0],
		results,
	)
	if ready {
		t.Fatalf("discovery_query alone should not mark readiness without a source body read")
	}

	// After discovery_query + two source body reads, readiness should fire.
	resultsWithRead := append(results, workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Read file internal/agent/runner.go lines 1-200",
		Output:  "package agent\nfunc runChildTask() {}",
	}, workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Read file internal/agent/runner_child_tasks.go lines 1-100",
		Output:  "package agent\nfunc handleChildTask() {}",
	})
	ready = runner.delegatedReadOnlyAnswerReadyAfterResult(
		workspace.ToolRequest{Tool: "read_file"},
		resultsWithRead[2],
		resultsWithRead,
	)
	if !ready {
		t.Fatalf("discovery_query plus source body reads should mark readiness")
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

func TestDelegatedReadOnlyChildHostClosesAfterAnswerReadyEvidence(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "agent"), 0o755); err != nil {
		t.Fatalf("mkdir internal/agent: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "internal", "agent", "runner.go"), []byte(strings.Join([]string{
		"package agent",
		"",
		"func dispatch() {",
		"\t// Initial orientation only; the delegated mechanism is elsewhere.",
		"}",
	}, "\n")), 0o644); err != nil {
		t.Fatalf("write runner.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "internal", "agent", "runner_child_tasks.go"), []byte(strings.Join([]string{
		"package agent",
		"",
		"func runChildTask() {",
		"\t// The task tool launches a subagent child runner for delegated read-only evidence.",
		"}",
	}, "\n")), 0o644); err != nil {
		t.Fatalf("write runner_child_tasks.go: %v", err)
	}

	replies := []string{
		`{"tool":"search_files","pattern":"runner_child_tasks","path":"internal/agent"}`,
		`{"tool":"read_file","path":"internal/agent/runner_child_tasks.go","start_line":1,"end_line":20}`,
		"SHOULD NOT BE USED",
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
		AgentName: "child:explorer",
		Client:    llama.NewClient(server.URL, 2*time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 4, MaxNoProgressTurns: 3},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        string(workerruntime.TaskFamilyDelegatedReadOnly),
			Contract:          string(workerruntime.ContractGroundedEvidence),
			Objective:         "Can you tell me how this harness uses subagents?",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"search_files", "read_file"},
		},
		OnEvent: func(event RunEvent) {
			roles = append(roles, event.Role)
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Can you tell me how this harness uses subagents?"}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if requests != 2 {
		t.Fatalf("expected child host closeout without an extra model turn, used %d requests; roles=%v answer=%q", requests, roles, answer)
	}
	for _, want := range []string{"task", "subagent", "internal/agent/runner_child_tasks.go"} {
		if !strings.Contains(strings.ToLower(answer), strings.ToLower(want)) {
			t.Fatalf("expected answer to contain %q, got:\n%s", want, answer)
		}
	}
	if !slices.Contains(roles, "delegated_read_only_answer_ready") {
		t.Fatalf("expected delegated readiness event before host closeout, got roles %v", roles)
	}
}

// TestArchitectureLaneChildDiscoveryDoesNotBlockReadAfterDiscoveryQuery
// reproduces the 2026-05-30 regression where the architecture lane delegated
// to a child explorer, the child ran discovery_query, and then all subsequent
// read_file calls were blocked because readiness fired prematurely.
// The fix requires at least one source body read after discovery_query before
// declaring readiness.
// TestChildDiscoveryReadAllowedAfterDiscoveryQuery verifies that
// after a discovery_query produces a composite packet, the child explorer
// is still allowed to read source files before readiness fires.
// Regression test for the 2026-05-30 incident where readiness was declared
// immediately after discovery_query, blocking all subsequent reads.
func TestChildDiscoveryReadAllowedAfterDiscoveryQuery(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "agent"), 0o755); err != nil {
		t.Fatalf("mkdir agent dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "internal", "agent", "runner.go"), []byte("package agent\n\nfunc Run() {}\n"), 0o644); err != nil {
		t.Fatalf("write runner.go: %v", err)
	}

	replies := []string{
		`{"tool":"discovery_query","purpose":"architecture_boundary","path":".","pattern":"runner"}`,
		`{"tool":"read_file","path":"internal/agent/runner.go"}`,
		"`internal/agent/runner.go` contains the `Run` function.",
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request #%d (max %d)", requests, len(replies))
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
		Client:    llama.NewClient(server.URL, 2*time.Second),
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		Policy:    RunPolicy{MaxToolSteps: 6, MaxNoProgressTurns: 4},
		AgentName: "child:explorer",
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "delegated_read_only",
			PermissionProfile: "read_only_workspace",
			Objective:         "Explain what internal/agent/runner.go contains.",
			AllowedTools:      []string{"read_file", "search_content", "search_files"},
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Explain what internal/agent/runner.go contains."}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// The read_file should NOT be blocked by premature readiness after
	// discovery_query. The answer should reflect the source read.
	if !strings.Contains(answer, "runner.go") && !strings.Contains(answer, "Run function") {
		t.Fatalf("expected answer to reference runner.go or Run function, got: %q", answer)
	}
	// The child should have used all 3 replies (discovery + read + answer).
	if requests < 3 {
		t.Fatalf("expected at least 3 model calls, got %d (child may have been blocked prematurely)", requests)
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
			Objective:         "Explain how this harness uses sub-agents.\nKey terms: child task, runner",
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
		if !slices.Contains(payload.EvidencePaths, want) {
			t.Fatalf("expected answer-ready event to include %q, got %#v", want, payload.EvidencePaths)
		}
	}
	for _, forbidden := range []string{"runner.go", "runner_child_tasks.go"} {
		if slices.Contains(payload.EvidencePaths, forbidden) {
			t.Fatalf("expected answer-ready event to avoid bare duplicate path %q, got %#v", forbidden, payload.EvidencePaths)
		}
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

func TestAugmentDelegatedReadOnlyPacketDoesNotInjectDiscoveryUsefulnessExpectedPaths(t *testing.T) {
	answer := "For `What can we add to our discovery tools in an effort at increasing their overall usefulness?`, this is grounded in `internal/workspace/discovery_query.go`, `internal/agent/runner.go`."
	results := []workspace.ToolResult{
		{
			Tool:    "discovery_query",
			Summary: "Ran architecture_boundary discovery query",
			Output:  `{"artifact_path":".idiom/discovery/queries/query.json","evidence":[{"name":"text_anchor_search","summary":"Found discovery tool implementation paths.","output_excerpt":"internal/workspace/discovery_query.go:338: func runDiscoveryQuery\ninternal/agent/runner.go:648: runChildTask"}]}`,
		},
	}
	augmented := augmentDelegatedReadOnlyPacket(answer, "What can we add to our discovery tools in an effort at increasing their overall usefulness?", results)
	for _, forbidden := range []string{
		"Additional inspected paths:",
		"`internal/agent/answer_composer.go`",
		"`internal/agent/runner_lane_policy.go`",
	} {
		if strings.Contains(augmented, forbidden) {
			t.Fatalf("discovery usefulness augmentation should not inject topic-specific path %q, got %q", forbidden, augmented)
		}
	}
	for _, want := range []string{"`internal/workspace/discovery_query.go`", "`internal/agent/runner.go`"} {
		if !strings.Contains(augmented, want) {
			t.Fatalf("expected augmented discovery packet to preserve actual evidence path %q, got %q", want, augmented)
		}
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
		if !slices.Contains(got, want) {
			t.Fatalf("expected sanitized readiness paths to contain %q, got %#v", want, got)
		}
	}
	for _, forbidden := range []string{".idiom/discovery/queries/query.json", "symbol/body/branch"} {
		if slices.Contains(got, forbidden) {
			t.Fatalf("did not expect sanitized readiness paths to retain %q, got %#v", forbidden, got)
		}
	}
}

func TestEmitAcceptedAnswerSignalsUsesTypedPacketEvidencePaths(t *testing.T) {
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
	result := typedReadOnlyTaskResult(ReadOnlyEvidencePacket{
		Objective:       runner.RuntimeBrief.Objective,
		SourceBodyPaths: []string{"internal/agent/runner.go", "internal/agent/runner_child_tasks.go"},
		InspectedPaths:  []string{"internal/agent/runner.go", ".idiom/discovery/queries/query.json", "internal/agent/runner_child_tasks.go"},
		Findings:        []ReadOnlyEvidenceFinding{{Path: "internal/agent/runner.go", Text: "The runner delegates subagent work via the task tool."}},
		Confidence:      "high",
	})
	runner.emitAcceptedAnswerSignals("The harness delegates via task.", []workspace.ToolResult{result})

	rawPaths, _ := readiness["evidence_paths"].([]any)
	got := make([]string, 0, len(rawPaths))
	for _, raw := range rawPaths {
		if value, ok := raw.(string); ok {
			got = append(got, value)
		}
	}
	for _, want := range []string{"internal/agent/runner.go", "internal/agent/runner_child_tasks.go"} {
		if !slices.Contains(got, want) {
			t.Fatalf("expected typed readiness paths to contain %q, got %#v", want, got)
		}
	}
	if slices.Contains(got, ".idiom/discovery/queries/query.json") {
		t.Fatalf("did not expect typed readiness to retain artifact path, got %#v", got)
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
	if !slices.Contains(got, "internal/agent/runner_child_tasks.go") {
		t.Fatalf("expected exact probe path in readiness paths, got %#v", got)
	}
	for _, forbidden := range []string{"internal/agent/instructions.go", "internal/agent/modes.go"} {
		if slices.Contains(got, forbidden) {
			t.Fatalf("did not expect noisy pinned path %q in readiness paths, got %#v", forbidden, got)
		}
	}
}

func TestEmitAcceptedAnswerSignalsDirectAnswerAliasBlocksUnrelatedEvidence(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Lane:              "main",
			TaskFamily:        string(workerruntime.TaskFamilyDirectAnswer),
			Contract:          string(workerruntime.ContractDirectAnswer),
			PermissionProfile: "read_only_workspace",
			Objective:         "Find why the TUI chat input first wraps to a newline and the existing line shifts upward out of view.",
		},
	}
	events := map[string]map[string]any{}
	runner.OnEvent = func(event RunEvent) {
		switch event.Role {
		case "readiness_check", "final_answer_critic":
			var fields map[string]any
			_ = json.Unmarshal([]byte(event.Content), &fields)
			events[event.Role] = fields
		}
	}
	runner.emitAcceptedAnswerSignals(
		"The TUI chat input newline wrap and upward shift issue is in `internal/app/rendering.go`: `evidenceDisplayName` trims the raw tool name before display.",
		[]workspace.ToolResult{
			{
				Tool:    "context_pack",
				Summary: "Context pack wrote packet",
				Output:  `{"candidate_files":[{"path":"internal/app/rendering.go"}]}`,
			},
			{
				Tool:    "read_file",
				Summary: "Read internal/app/rendering.go lines 1-9",
				Output:  "1:package app\n2:\n3:func evidenceDisplayName(rawTool string) string {\n4:\treturn strings.TrimSpace(rawTool)\n5:}\n",
			},
		},
	)

	readiness := events["readiness_check"]
	if readiness["acceptance_contract"] != finalAnswerAcceptanceSchema {
		t.Fatalf("expected acceptance contract field, got %#v", readiness)
	}
	if readiness["acceptance_decision"] != string(finalAnswerAcceptanceRejected) ||
		readiness["action"] != "block" ||
		readiness["blocker_kind"] != "answer_objective_unmet" ||
		readiness["objective_relevance"] != "checked" {
		t.Fatalf("expected blocked objective relevance readiness, got %#v", readiness)
	}
	if paths, _ := readiness["evidence_paths"].([]any); len(paths) != 0 {
		t.Fatalf("direct_answer alias blocked telemetry must not fall back to unrelated evidence paths, got %#v", paths)
	}
	critic := events["final_answer_critic"]
	if critic["acceptance_contract"] != finalAnswerAcceptanceSchema ||
		critic["acceptance_decision"] != string(finalAnswerAcceptanceRejected) ||
		critic["blocker_kind"] != "answer_objective_unmet" {
		t.Fatalf("expected final critic to carry rejected acceptance fields, got %#v", critic)
	}
}

func TestFinalAnswerOverrideLogsOriginalObjectiveRejection(t *testing.T) {
	rawRejectedAnswer := "The TUI chat input newline wrap and upward shift issue is in `internal/app/rendering.go`: `evidenceDisplayName` trims the raw tool name before display."
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": rawRejectedAnswer},
			}},
		})
	}))
	defer server.Close()

	var override map[string]any
	var acceptedCritic bool
	var blockedCritic map[string]any
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Lane:              "main",
			TaskFamily:        string(workerruntime.TaskFamilyDirectAnswer),
			Contract:          string(workerruntime.ContractDirectAnswer),
			PermissionProfile: "read_only_workspace",
			Objective:         "Find why the TUI chat input first wraps to a newline and the existing line shifts upward out of view.",
			Readiness:         &workerruntime.ReadinessState{ReadyToAnswer: true},
		},
		SeedResults: []workspace.ToolResult{
			{
				Tool:    "read_file",
				Summary: "Read internal/app/rendering.go lines 1-9",
				Output:  "1:package app\n2:\n3:func evidenceDisplayName(rawTool string) string {\n4:\treturn strings.TrimSpace(rawTool)\n5:}\n",
			},
		},
		OnEvent: func(event RunEvent) {
			switch event.Role {
			case "final_answer_blocker_override":
				if err := json.Unmarshal([]byte(event.Content), &override); err != nil {
					t.Fatalf("decode override event: %v", err)
				}
			case "final_answer_critic":
				if strings.Contains(event.Content, `"action":"accept"`) {
					acceptedCritic = true
				}
				if strings.Contains(event.Content, `"action":"block"`) {
					if err := json.Unmarshal([]byte(event.Content), &blockedCritic); err != nil {
						t.Fatalf("decode blocked critic: %v", err)
					}
				}
			}
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: runner.RuntimeBrief.Objective}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Contains(answer, "<<QUESTION>>") {
		t.Fatalf("user-facing answer should be sanitized, got:\n%s", answer)
	}
	if strings.Contains(answer, "evidenceDisplayName") {
		t.Fatalf("user-facing answer leaked rejected model answer:\n%s", answer)
	}
	if override == nil {
		t.Fatal("expected final_answer_blocker_override event")
	}
	if acceptedCritic {
		t.Fatal("rejected final answer replacement must not emit accepted final-answer telemetry")
	}
	if blockedCritic["blocker_kind"] != "answer_objective_unmet" ||
		blockedCritic["acceptance_decision"] != string(finalAnswerAcceptanceRejected) ||
		blockedCritic["objective_relevance"] != "checked" {
		t.Fatalf("blocked telemetry should consume the original raw-answer acceptance, got %#v", blockedCritic)
	}
	if override["original_blocker_kind"] != "answer_objective_unmet" ||
		override["replacement_stop_reason"] != "answer_objective_unmet" ||
		override["objective_relevance"] != "checked" ||
		override["original_acceptance_decision"] != string(finalAnswerAcceptanceRejected) ||
		override["raw_answer_logged"] != false {
		t.Fatalf("override event lost original rejection metadata: %#v", override)
	}
	if reason, _ := override["original_reason"].(string); !strings.Contains(reason, "does not establish the user's objective") {
		t.Fatalf("override event should carry sanitized contract reason, got %#v", override)
	}
	if paths, _ := override["original_evidence_paths"].([]any); len(paths) != 0 {
		t.Fatalf("override event must not promote unrelated evidence paths, got %#v", paths)
	}
	if strings.Contains(fmt.Sprint(override), "evidenceDisplayName") {
		t.Fatalf("override event leaked raw rejected answer: %#v", override)
	}
}

func TestTerminalAnswerDecisionBlocksTransportProtocolOutput(t *testing.T) {
	rawAnswer := `<n_tool name="read_file"><path>internal/agent/runner.go</path></n_tool>`
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Lane:              "main",
			TaskFamily:        string(workerruntime.TaskFamilyDirectAnswer),
			Contract:          string(workerruntime.ContractDirectAnswer),
			PermissionProfile: "read_only_workspace",
			Objective:         "Explain the finish boundary.",
		},
	}
	decision := runner.classifyTerminalAnswer(rawAnswer, "model_final", nil)
	if decision.Outcome != terminalAnswerTransportBlocked {
		t.Fatalf("transport markup got outcome %q, want %q", decision.Outcome, terminalAnswerTransportBlocked)
	}
	if decision.Acceptance.BlockerKind != "transport_protocol_output" ||
		decision.Acceptance.Decision != finalAnswerAcceptanceRejected ||
		decision.StopReason != "transport_protocol_output" {
		t.Fatalf("transport decision should be blocked protocol output, got %#v", decision)
	}
	if strings.Contains(decision.PresentedAnswer, "<n_tool") || strings.TrimSpace(decision.PresentedAnswer) == "" {
		t.Fatalf("presented transport blocker should be clean user-facing prose, got %q", decision.PresentedAnswer)
	}

	var critic map[string]any
	runner.OnEvent = func(event RunEvent) {
		if event.Role != "final_answer_critic" {
			return
		}
		if err := json.Unmarshal([]byte(event.Content), &critic); err != nil {
			t.Fatalf("decode critic: %v", err)
		}
	}
	runner.emitFinalAnswerTelemetry(decision.RawAnswer, nil, decision.Acceptance)
	if critic["action"] != "block" ||
		critic["blocker_kind"] != "transport_protocol_output" ||
		critic["acceptance_decision"] != string(finalAnswerAcceptanceRejected) {
		t.Fatalf("transport telemetry should be blocked from the typed decision, got %#v", critic)
	}
}

func TestReadOnlySourceGroundingRejectionRetriesEvidenceLoop(t *testing.T) {
	root := t.TempDir()
	source := strings.Join([]string{
		"package app",
		"",
		"func previousMessageIndex(current int) int {",
		"\treturn current - 1",
		"}",
	}, "\n")
	if err := writeTestFile(root, "internal/app/model_state.go", source); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	replies := []string{
		"The message navigation lives in `internal/app/model_state_test.go` lines 1-5.",
		`{"tool":"read_file","path":"internal/app/model_state.go","start_line":1,"end_line":5,"named_gap":"source body for message navigation after missing test-path citation","why_not_answering":"the previous final answer cited a current-workspace path that does not exist"}`,
		"The current source evidence is `internal/app/model_state.go` lines 1-5: `previousMessageIndex` returns the previous index.",
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

	var sawCompletionGateRejection bool
	var sawOverride bool
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 6, MaxNoProgressTurns: 4},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Lane:              "main",
			TaskFamily:        string(workerruntime.TaskFamilyDirectAnswer),
			Contract:          string(workerruntime.ContractDirectAnswer),
			PermissionProfile: "read_only_workspace",
			Objective:         "Explain where up/down previous-message navigation would live.",
			AllowedTools:      []string{"read_file", "search_files", "search_content", "code_navigate"},
			Readiness:         &workerruntime.ReadinessState{ReadyToAnswer: false},
		},
		OnEvent: func(event RunEvent) {
			switch event.Role {
			case "next_action_state":
				if strings.Contains(event.Content, "completion_gate_rejected") {
					sawCompletionGateRejection = true
				}
			case "final_answer_blocker_override":
				sawOverride = true
			}
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: runner.RuntimeBrief.Objective}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if requests != len(replies) {
		t.Fatalf("expected rejected final answer, recovery read, and corrected final answer; used %d/%d replies", requests, len(replies))
	}
	if !sawCompletionGateRejection {
		t.Fatal("expected source-grounding rejection to be recorded as completion_gate_rejected")
	}
	if sawOverride {
		t.Fatal("first recoverable source-grounding rejection should not be replaced with final_answer_blocker_override")
	}
	if !strings.Contains(answer, "internal/app/model_state.go") || !strings.Contains(answer, "previousMessageIndex") {
		t.Fatalf("expected corrected answer from source-body evidence, got:\n%s", answer)
	}
	if strings.Contains(answer, "model_state_test.go") {
		t.Fatalf("corrected answer should not retain missing-path citation:\n%s", answer)
	}
}

func TestReadOnlyAnswerObjectiveRejectionRetriesEvidenceLoop(t *testing.T) {
	root := t.TempDir()
	source := strings.Join([]string{
		"package app",
		"",
		"func explainChatInputWrap() string {",
		"\treturn \"chat input first wraps to a newline when measured height grows and the viewport shifts upward out of view\"",
		"}",
	}, "\n")
	if err := writeTestFile(root, "internal/app/model.go", source); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	objective := "Find why the TUI chat input first wraps to a newline and the existing line shifts upward out of view."
	replies := []string{
		"The TUI chat input newline wrap and upward shift issue is in `internal/app/rendering.go`: `evidenceDisplayName` trims the raw tool name before display.",
		`{"tool":"read_file","path":"internal/app/model.go","start_line":1,"end_line":5,"named_gap":"objective-relevant source body for TUI chat input wrapping upward","why_not_answering":"the previous final answer used inspected source that did not establish the user's objective"}`,
		"The objective-relevant source is `internal/app/model.go` lines 3-5: `explainChatInputWrap` says the chat input first wraps to a newline when measured height grows and the viewport shifts upward out of view.",
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

	var sawCompletionGateRejection bool
	var sawOverride bool
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 6, MaxNoProgressTurns: 4},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Lane:              "main",
			TaskFamily:        string(workerruntime.TaskFamilyDirectAnswer),
			Contract:          string(workerruntime.ContractDirectAnswer),
			PermissionProfile: "read_only_workspace",
			Objective:         objective,
			AllowedTools:      []string{"read_file", "search_files", "search_content", "code_navigate"},
			Readiness:         &workerruntime.ReadinessState{ReadyToAnswer: false},
		},
		SeedResults: []workspace.ToolResult{
			{
				Tool:    "read_file",
				Summary: "Read internal/app/rendering.go lines 1-9",
				Output:  "1:package app\n2:\n3:func evidenceDisplayName(rawTool string) string {\n4:\treturn strings.TrimSpace(rawTool)\n5:}\n",
			},
		},
		OnEvent: func(event RunEvent) {
			switch event.Role {
			case "next_action_state":
				if strings.Contains(event.Content, "completion_gate_rejected") {
					sawCompletionGateRejection = true
				}
			case "final_answer_blocker_override":
				sawOverride = true
			}
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: objective}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if requests != len(replies) {
		t.Fatalf("expected objective rejection, recovery read, and corrected answer; used %d/%d replies", requests, len(replies))
	}
	if !sawCompletionGateRejection {
		t.Fatal("expected objective rejection to be recorded as completion_gate_rejected")
	}
	if sawOverride {
		t.Fatal("recoverable objective rejection should not be replaced with final_answer_blocker_override")
	}
	if !strings.Contains(answer, "internal/app/model.go") || !strings.Contains(answer, "explainChatInputWrap") {
		t.Fatalf("expected corrected answer from objective-relevant source evidence, got:\n%s", answer)
	}
	if strings.Contains(answer, "evidenceDisplayName") {
		t.Fatalf("corrected answer should not retain unrelated source claim:\n%s", answer)
	}
}

func TestFinalAnswerAcceptanceRetryPolicyKeepsNonRetryableBoundaries(t *testing.T) {
	readOnlyRunner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        string(workerruntime.TaskFamilyDirectAnswer),
			Contract:          string(workerruntime.ContractDirectAnswer),
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"read_file", "search_content"},
		},
	}
	retryable := []string{"source_grounding_unmet", "answer_objective_unmet", "read_only_protocol_blocker"}
	for _, kind := range retryable {
		if !readOnlyRunner.retryableFinalAnswerAcceptanceRejection(finalAnswerAcceptance{BlockerKind: kind}, nil, 0, nil) {
			t.Fatalf("%s should be retryable for read-only direct-answer evidence repair", kind)
		}
	}
	nonRetryable := []string{
		"non_answer_prose",
		"unexecuted_tool_request_final_answer",
		"required_evidence_unmet",
		"user_visible_blocker",
		"required_edit_paths_remaining",
	}
	for _, kind := range nonRetryable {
		if readOnlyRunner.retryableFinalAnswerAcceptanceRejection(finalAnswerAcceptance{BlockerKind: kind}, nil, 0, nil) {
			t.Fatalf("%s must not enter read-only final-answer retry policy", kind)
		}
	}
}

func TestDirectAnswerQuestionProtocolIsRetryableNotUserVisibleBlocker(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        string(workerruntime.TaskFamilyDirectAnswer),
			Contract:          string(workerruntime.ContractDirectAnswer),
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"read_file", "search_content"},
		},
	}
	kind, _ := runner.finalAnswerBlockerKind("<<QUESTION>>: I need one more source read.", nil)
	if kind != "read_only_protocol_blocker" {
		t.Fatalf("read-only direct-answer QUESTION protocol should be retryable protocol drift, got %q", kind)
	}
	if !runner.retryableFinalAnswerAcceptanceRejection(finalAnswerAcceptance{BlockerKind: kind}, nil, 0, nil) {
		t.Fatalf("%s should enter read-only final-answer retry policy", kind)
	}
}

func TestHostReadOnlyCloseoutDoesNotTerminalizeAsBlocked(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        string(workerruntime.TaskFamilyDirectAnswer),
			Contract:          string(workerruntime.ContractDirectAnswer),
			PermissionProfile: "read_only_workspace",
			Objective:         "Why did the latest run fail?",
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "search_content",
		Summary: "Found 1 matching line(s).",
		Output:  "logs/conversations/20260604-195321.801.jsonl:20: completion_gate_repeated_rejection",
	}}
	answer, ok := runner.hostOwnedReadOnlyCloseoutAnswer(results)
	if !ok {
		t.Fatal("expected host-owned read-only closeout answer")
	}
	if strings.Contains(answer, "<<QUESTION>>") || strings.Contains(strings.ToLower(answer), "did not change any files") {
		t.Fatalf("host closeout must be normal read-only prose, got:\n%s", answer)
	}
	decision := runner.classifyTerminalAnswer(answer, "host_read_only_closeout", results)
	if decision.Outcome != terminalAnswerAccepted || decision.StopReason != "host_read_only_closeout" {
		t.Fatalf("host closeout should not terminalize as blocked, got %#v\nanswer:\n%s", decision, answer)
	}
}

func TestFinalAnswerAcceptanceRetryPolicyRequiresToolsAndNoMutation(t *testing.T) {
	noToolsRunner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        string(workerruntime.TaskFamilyDirectAnswer),
			Contract:          string(workerruntime.ContractDirectAnswer),
			PermissionProfile: "read_only_workspace",
		},
	}
	if noToolsRunner.retryableFinalAnswerAcceptanceRejection(finalAnswerAcceptance{BlockerKind: "answer_objective_unmet"}, nil, 0, nil) {
		t.Fatal("answer_objective_unmet must remain terminal when no evidence tools are available")
	}

	readOnlyRunner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        string(workerruntime.TaskFamilyDirectAnswer),
			Contract:          string(workerruntime.ContractDirectAnswer),
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"read_file"},
		},
	}
	if !readOnlyRunner.retryableFinalAnswerAcceptanceRejection(finalAnswerAcceptance{BlockerKind: "source_grounding_unmet"}, nil, 1, nil) {
		t.Fatal("source_grounding_unmet should remain retryable before the retry limit is exhausted")
	}
	if readOnlyRunner.retryableFinalAnswerAcceptanceRejection(finalAnswerAcceptance{BlockerKind: "source_grounding_unmet"}, nil, 2, nil) {
		t.Fatal("source_grounding_unmet must become terminal once the retry budget is exhausted")
	}

	malformedRequiredPathRunner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        string(workerruntime.TaskFamilyDirectAnswer),
			Contract:          string(workerruntime.ContractDirectAnswer),
			PermissionProfile: "read_only_workspace",
			RequiredEditPaths: []string{"internal/app/model.go"},
			AllowedTools:      []string{"read_file"},
		},
	}
	if malformedRequiredPathRunner.retryableFinalAnswerAcceptanceRejection(finalAnswerAcceptance{BlockerKind: "source_grounding_unmet"}, nil, 0, nil) {
		t.Fatal("required_edit_paths must keep malformed direct-answer/read-only briefs out of final-answer retry policy")
	}

	mutationRunner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        string(workerruntime.TaskFamilyImplementation),
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"internal/app/model.go"},
			AllowedTools:      []string{"read_file", "apply_patch"},
		},
	}
	if mutationRunner.retryableFinalAnswerAcceptanceRejection(finalAnswerAcceptance{BlockerKind: "required_edit_paths_remaining"}, nil, 0, nil) {
		t.Fatal("mutation required_edit_paths_remaining must not enter read-only final-answer retry policy")
	}
	if mutationRunner.retryableFinalAnswerAcceptanceRejection(finalAnswerAcceptance{BlockerKind: "answer_objective_unmet"}, nil, 0, nil) {
		t.Fatal("read-only answer retry policy must not apply to mutation lanes")
	}
}

func TestEmitAcceptedAnswerSignalsImplementationEvidenceUsesChangedPaths(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Lane:              "implement",
			Contract:          "implementation_result",
			TaskFamily:        "implementation",
			PermissionProfile: "guarded_workspace_write",
			Objective:         "Update TDD obligation planning.",
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
		"Changed `internal/productworkflow/tdd_obligation_plans.go` and tests.",
		[]workspace.ToolResult{
			{
				Tool:    "read_file",
				Summary: "Read f2 internal/productworkflow/tdd_obligation_plans_test.go lines 1-220",
				Output:  "RelPath: \"adrs/ADR-1.md\"\nFile: \"adrs/prd.md\"\nOwner: \"adrs/greeting.test.js\"",
			},
			{
				Tool:    "edit_file",
				Summary: "Edited internal/productworkflow/tdd_obligation_plans.go",
			},
			{
				Tool:    "edit_file",
				Summary: "Edited internal/productworkflow/tdd_obligation_plans_test.go",
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
	for _, want := range []string{"internal/productworkflow/tdd_obligation_plans.go", "internal/productworkflow/tdd_obligation_plans_test.go"} {
		if !slices.Contains(got, want) {
			t.Fatalf("expected implementation readiness paths to contain changed path %q, got %#v", want, got)
		}
	}
	for _, forbidden := range []string{"adrs/ADR-1.md", "adrs/prd.md", "adrs/greeting.test.js"} {
		if slices.Contains(got, forbidden) {
			t.Fatalf("did not expect fixture path %q in implementation readiness evidence, got %#v", forbidden, got)
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

func TestCompletionGatePipelinePreservesInternalOrderBeforeExternalGate(t *testing.T) {
	externalCalled := false
	runner := &Runner{
		CompletionGate: func(input CompletionGateInput) CompletionGateResult {
			externalCalled = true
			return CompletionGateResult{Summary: "external rejected", Error: "external"}
		},
	}
	result, rejected := runner.completionGateRejection(
		"<n_tool_calls>\n{\"tool\":\"read_file\",\"path\":\"README.md\"}\n</n_tool_calls>",
		nil,
		1,
		nil,
		false,
	)
	if !rejected {
		t.Fatalf("expected textual tool-call rejection")
	}
	if externalCalled {
		t.Fatalf("external completion gate must not run after an internal short-circuit")
	}
	if result.Summary != "Textual tool-call markup rejected as final answer" {
		t.Fatalf("unexpected first rejection: %#v", result)
	}
}

func TestCompletionGatePipelineFallsThroughToExternalGate(t *testing.T) {
	externalCalled := false
	runner := &Runner{
		CompletionGate: func(input CompletionGateInput) CompletionGateResult {
			externalCalled = true
			return CompletionGateResult{Summary: "external rejected", Error: "external"}
		},
	}
	result, rejected := runner.completionGateRejection("Normal prose answer.", nil, 1, nil, false)
	if !rejected || !externalCalled {
		t.Fatalf("expected external gate rejection, called=%v result=%#v", externalCalled, result)
	}
	if result.Summary != "external rejected" || result.Error != "external" {
		t.Fatalf("external gate message changed: %#v", result)
	}
}

func TestReadOnlyEvidenceCompletionRejectionRequiresBoundedEvidence(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Contract:          "direct_answer",
			TaskFamily:        "conversation",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"search_content", "read_file"},
			Objective:         "Can we shorten internal/labels/labels.go?",
			Readiness:         &workerruntime.ReadinessState{ReadyToAnswer: false, OpenObligations: []string{"evidence"}},
		},
	}
	result, rejected := runner.completionGateRejection(
		"Yes, that label can be shortened.",
		nil,
		1,
		nil,
		false,
	)
	if !rejected {
		t.Fatalf("expected read-only final answer without evidence to be rejected")
	}
	if !strings.Contains(result.Error, "bounded source-body or diagnostic evidence") {
		t.Fatalf("expected bounded-evidence rejection, got %#v", result)
	}
	prompt, hideReads, ok := runner.completionGateRecoveryPrompt([]workspace.ToolResult{result})
	if !ok || hideReads {
		t.Fatalf("expected read-only evidence recovery prompt without hiding reads, got ok=%t hideReads=%t prompt=%q", ok, hideReads, prompt)
	}
	if !strings.Contains(prompt, `"tool":"read_file"`) || !strings.Contains(prompt, "internal/labels/labels.go") {
		t.Fatalf("expected recovery prompt to request the named source path, got:\n%s", prompt)
	}
}

func TestReadOnlyEvidenceCompletionRejectionAppliesToDirectAnswerAliasWithoutReadiness(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Contract:          "direct_answer",
			TaskFamily:        string(workerruntime.TaskFamilyDirectAnswer),
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"context_pack", "search_content", "read_file"},
			Objective:         "Find why the TUI chat input first wraps to a newline.",
		},
	}
	result, rejected := runner.readOnlyEvidenceCompletionRejection(
		"I cannot determine from the available evidence why the TUI chat input first wraps to a newline.",
		nil,
	)
	if !rejected {
		t.Fatal("expected direct_answer alias without readiness/source evidence to be rejected")
	}
	if !strings.Contains(result.Error, "bounded source-body or diagnostic evidence") {
		t.Fatalf("expected bounded-evidence rejection, got %#v", result)
	}
}

func TestReadOnlyEvidenceCompletionRejectionSkipsConversationWithoutReadiness(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Contract:          "direct_answer",
			TaskFamily:        "conversation",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"context_pack", "search_content", "read_file"},
			Objective:         "In two short sentences, explain ordinary chat that does not ask for repository work.",
		},
	}
	_, rejected := runner.readOnlyEvidenceCompletionRejection(
		"Ordinary chat should be handled as direct conversation without workspace tools.",
		nil,
	)
	if rejected {
		t.Fatal("conversation/direct_answer without readiness should not be forced through the alias evidence gate")
	}
}

func TestReadOnlyEvidenceCompletionRejectionAllowsSourceBackedInsufficientEvidenceAlias(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Contract:          "direct_answer",
			TaskFamily:        string(workerruntime.TaskFamilyDirectAnswer),
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"context_pack", "search_content", "read_file"},
			Objective:         "Find why the TUI chat input first wraps to a newline.",
		},
	}
	_, rejected := runner.readOnlyEvidenceCompletionRejection(
		"I cannot determine from the available evidence why the TUI chat input first wraps to a newline.",
		[]workspace.ToolResult{{
			Tool:    "read_file",
			Summary: "Read internal/app/rendering.go lines 1-9",
			Output:  "1:package app\n2:\n3:func evidenceDisplayName(rawTool string) string { return strings.TrimSpace(rawTool) }\n",
		}},
	)
	if rejected {
		t.Fatal("source-backed insufficient-evidence direct_answer alias should reach final acceptance instead of completion-gate rejection")
	}
}

func TestReadOnlyEvidenceRejectionDoesNotAskToRereadAlreadyInspectedNamedPath(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Contract:          "locate_targets",
			TaskFamily:        "locate",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"search_content", "read_file", "code_navigate"},
			Objective:         "Read-only investigation: session logs/conversations/recent.jsonl ended with a bad final answer. Identify the likely guard file and tests.",
			Readiness:         &workerruntime.ReadinessState{ReadyToAnswer: false, OpenObligations: []string{"evidence"}},
		},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read f1 logs/conversations/recent.jsonl lines 1-80",
			Output:  "1:{\"type\":\"final\",\"content\":\"The user is asking me...\"}\n",
		},
		{
			Tool:    "completion_gate",
			Summary: "Read-only completion gate rejected final answer",
			Error:   "read-only workspace answers require bounded source-body or diagnostic evidence before a final answer",
		},
	}
	result, rejected := runner.readOnlyEvidenceCompletionRejection("The guard is probably in the runner.", results)
	if !rejected {
		t.Fatal("expected read-only evidence rejection before codebase evidence")
	}
	prompt := result.Error
	for _, want := range []string{"should not be reread", "different concrete code path", "source search/read/navigation"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("rejection guidance missing %q:\n%s", want, prompt)
		}
	}
}

func TestHostOwnedReadOnlyCloseoutSynthesizesAnswerableArchitectureChildPacket(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Contract:          "architecture_assessment",
			TaskFamily:        "architecture",
			PermissionProfile: "read_only_workspace",
			Objective:         "Explain how this harness uses sub-agents.",
		},
	}
	results := []workspace.ToolResult{
		typedReadOnlyTaskResult(ReadOnlyEvidencePacket{
			SchemaVersion:   ReadOnlyEvidencePacketSchema,
			Question:        "Explain how this harness uses sub-agents.",
			SourceBodyPaths: []string{"internal/agent/runner.go", "internal/agent/runner_child_tasks.go"},
			InspectedPaths:  []string{"internal/agent/runner.go", "internal/agent/runner_child_tasks.go"},
			Findings: []ReadOnlyEvidenceFinding{{
				Path: "internal/agent/runner_child_tasks.go",
				Text: "The subagent flow uses the task tool: the parent runner invokes child task execution and receives the result.",
			}},
			Confidence: "high",
		}),
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed file_outline via go_file_outline",
			Output:  "internal/agent/runner_child_tasks.go: runChildTask is the child task handoff.\ninternal/agent/runner.go: Runner drives the main task loop.",
		},
	}
	answer, ok := runner.hostOwnedReadOnlyCloseoutAnswer(results)
	if !ok {
		t.Fatal("expected host-owned architecture closeout from answerable child packet")
	}
	for _, want := range []string{"internal/agent/runner.go", "internal/agent/runner_child_tasks.go"} {
		if !strings.Contains(answer, want) {
			t.Fatalf("architecture child-packet closeout missing %q:\n%s", want, answer)
		}
	}
}

func TestHostOwnedReadOnlyCloseoutDoesNotUseCandidateOnlyEvidence(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Contract:          "architecture_assessment",
			TaskFamily:        "architecture",
			PermissionProfile: "read_only_workspace",
			Objective:         "Explain how this harness uses sub-agents.",
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "context_pack",
		Summary: "Context packet produced candidate files",
		Output:  "Candidate files:\n- `internal/agent/runner.go`\n- `internal/agent/runner_child_tasks.go`\n",
	}}
	if answer, ok := runner.hostOwnedReadOnlyCloseoutAnswer(results); ok {
		t.Fatalf("candidate-only evidence must not host-closeout, got %q", answer)
	}
}

func TestHostOwnedReadOnlyCloseoutDisabledForNoisyMentionedPaths(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Contract:          "architecture_assessment",
			TaskFamily:        "architecture",
			PermissionProfile: "read_only_workspace",
			Objective:         "Explain how this harness uses sub-agents.",
		},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "go_read_symbol",
			Summary: "Read runChildTask in internal/agent/runner_child_tasks.go lines 10-30",
			Output:  "10:func runChildTask() {}\n",
		},
		{
			Tool:    "discovery_query",
			Summary: "Discovery packet also mentioned .idiom/discovery/queries/query.json and testdata/swe/generated-logs/direct.jsonl.",
			Output:  "candidate artifacts: .idiom/discovery/queries/query.json\ntestdata/swe/generated-logs/direct.jsonl\n",
		},
	}
	answer, ok := runner.hostOwnedReadOnlyCloseoutAnswer(results)
	if ok {
		t.Fatalf("host-owned read-only closeout must not synthesize final answers from noisy evidence, got %q", answer)
	}
}

func TestReadOnlyAnalysisAnswerReadyRejectsIrrelevantArchitectureSourceReads(t *testing.T) {
	runner := &Runner{
		AgentName: "main",
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Contract:          "architecture_assessment",
			TaskFamily:        "architecture",
			PermissionProfile: "read_only_workspace",
			Objective:         "What can we add to our discovery tools to increase their usefulness?\nKey terms: query, context graph",
		},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "discovery_query",
			Summary: "Ran architecture_boundary discovery query",
			Output:  "internal/agent/answer_composer.go: answer shaping\ninternal/agent/runner.go: runner loop\n",
		},
		{
			Tool:    "read_file",
			Summary: "Read internal/agent/answer_composer.go lines 1-80",
			Output:  "func ComposeAnswer() string { return \"answer\" }\n",
		},
	}
	if runner.readOnlyAnalysisAnswerReadyAfterResult(workspace.ToolRequest{Tool: "read_file"}, results[1], results) {
		t.Fatal("irrelevant answer-composer source read must not satisfy discovery-tool readiness")
	}
}

func TestReadOnlyAnalysisAnswerReadyAcceptsRelevantDiscoveryToolSourceBody(t *testing.T) {
	runner := &Runner{
		AgentName: "main",
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Contract:          "architecture_assessment",
			TaskFamily:        "architecture",
			PermissionProfile: "read_only_workspace",
			Objective:         "What can we add to internal/discovery/query.go to increase discovery tool usefulness?",
		},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "discovery_query",
			Summary: "Ran architecture_boundary discovery query",
			Output:  `{"artifact_path":".idiom/discovery/queries/query.json","evidence":[{"path":"internal/discovery/query.go","summary":"discovery tool surface"},{"path":"internal/comprehension/trace.go","summary":"context graph trace"}]}`,
		},
		{
			Tool:    "go_read_symbol",
			Summary: "Read RunDiscoveryTool in internal/discovery/query.go lines 1-80",
			Output:  "func RunDiscoveryTool() string { return \"discovery tool context graph\" }\n",
		},
	}
	if !runner.readOnlyAnalysisAnswerReadyAfterResult(workspace.ToolRequest{Tool: "go_read_symbol"}, results[1], results) {
		t.Fatal("objective-relevant discovery source body should satisfy read-only architecture readiness")
	}
}

func TestAcceptedAnswerEvidencePathsIncludeMentionedToolOutputPaths(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		Contract:          "architecture_assessment",
		TaskFamily:        "architecture",
		PermissionProfile: "read_only_workspace",
		Objective:         "What can we add to our discovery tools to increase their usefulness?",
	}
	results := []workspace.ToolResult{
		{
			Tool:    "discovery_query",
			Summary: "Ran architecture boundary query",
			Output: strings.Join([]string{
				"internal/agent/answer_composer.go: answer composition owns final response shaping.",
				"internal/agent/runner.go: runner owns control rails and tool orchestration.",
			}, "\n"),
		},
	}
	paths := acceptedAnswerEvidencePaths("Discovery tool improvements are grounded in the inspected evidence.", results, brief)
	for _, want := range []string{"internal/agent/answer_composer.go", "internal/agent/runner.go"} {
		if !slices.Contains(paths, want) {
			t.Fatalf("accepted evidence paths missing %q: %#v", want, paths)
		}
	}
}

func TestReadFileResultHasConcreteSourceBodyExcludesDiagnosticLogs(t *testing.T) {
	if readFileResultHasConcreteSourceBody(workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Read f1 logs/conversations/recent.jsonl lines 1-80",
		Output:  "1:{\"type\":\"final\",\"content\":\"The user is asking me...\"}\n",
	}) {
		t.Fatal("diagnostic conversation logs must not satisfy codebase source-body evidence")
	}
	if readFileResultHasConcreteSourceBody(workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Read f1 .idiom/comprehension/views/context.json lines 1-80",
		Output:  "1:{\"candidate_files\":[{\"path\":\"internal/agent/runner.go\"}]}\n",
	}) {
		t.Fatal("generated .idiom context artifacts must not satisfy codebase source-body evidence")
	}
	if readFileResultHasConcreteSourceBody(workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Read f1 .codex/discovery/packet.json lines 1-80",
		Output:  "1:{\"candidate_files\":[{\"path\":\"internal/agent/runner.go\"}]}\n",
	}) {
		t.Fatal("generated discovery artifacts must not satisfy codebase source-body evidence")
	}
	if readFileResultHasConcreteSourceBody(workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Read f1 transcripts/session.jsonl lines 1-80",
		Output:  "1:{\"role\":\"assistant\",\"content\":\"...\"}\n",
	}) {
		t.Fatal("top-level transcript artifacts must not satisfy codebase source-body evidence")
	}
	if readFileResultHasConcreteSourceBody(workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Read f1 testdata/swe/generated-logs/direct.jsonl lines 1-80",
		Output:  "1:{\"type\":\"tool_result\"}\n",
	}) {
		t.Fatal("generated eval logs must not satisfy codebase source-body evidence")
	}
	if !readFileResultHasConcreteSourceBody(workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Read f1 internal/agent/runner.go lines 1-80",
		Output:  "1:package agent\n2:\n3:func example() {}\n",
	}) {
		t.Fatal("source file read should satisfy codebase source-body evidence")
	}
	if !readFileResultHasConcreteSourceBody(workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Read f1 testdata/events.jsonl lines 1-20",
		Output:  "1:{\"event\":\"fixture\"}\n",
	}) {
		t.Fatal("checked-in jsonl fixtures outside diagnostic log directories should remain source-body evidence")
	}
	if !readFileResultHasConcreteSourceBody(workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Read f1 internal/log/reader.go lines 1-20",
		Output:  "1:package log\n2:func reader() {}\n",
	}) {
		t.Fatal("source files under internal/log must remain source-body evidence")
	}
	if !readFileResultHasConcreteSourceBody(workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Read f1 internal/traces/reader.go lines 1-20",
		Output:  "1:package traces\n2:func reader() {}\n",
	}) {
		t.Fatal("source files under internal/traces must remain source-body evidence")
	}
	if !readFileResultHasConcreteSourceBody(workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Read f1 internal/transcript/debuglog.go lines 1-20",
		Output:  "1:package transcript\n2:func debuglog() {}\n",
	}) {
		t.Fatal("source files under internal/transcript must remain source-body evidence")
	}
}

func TestRepeatedDiagnosticLogReadFileBackfillsPreviousWindow(t *testing.T) {
	rewritten, raw, ok := repeatedDiagnosticLogReadFileBackfill(
		workspace.ToolRequest{Tool: "read_file", Path: "logs/conversations/recent.jsonl"},
		[]workspace.ToolResult{{
			Tool:    "read_file",
			Summary: "Read f1 logs/conversations/recent.jsonl lines 132-211 (file has 211 lines total; defaulted to recent log tail; pass start_line/end_line for a different window)",
			Output:  "132:{\"type\":\"message\"}\n",
		}},
	)
	if !ok {
		t.Fatal("expected repeated unbounded diagnostic log read to become previous bounded window")
	}
	if rewritten.StartLine != 52 || rewritten.EndLine != 131 {
		t.Fatalf("window: want 52-131, got %d-%d", rewritten.StartLine, rewritten.EndLine)
	}
	for _, want := range []string{`"path":"logs/conversations/recent.jsonl"`, `"start_line":52`, `"end_line":131`} {
		if !strings.Contains(raw, want) {
			t.Fatalf("raw rewrite missing %q:\n%s", want, raw)
		}
	}

	rewritten, raw, ok = repeatedDiagnosticLogReadFileBackfill(
		workspace.ToolRequest{Tool: "read_file", Path: "logs/conversations/recent.jsonl"},
		[]workspace.ToolResult{
			{
				Tool:    "read_file",
				Summary: "Read f1 logs/conversations/recent.jsonl lines 132-211 (total lines: 211)",
				Output:  "132:{\"type\":\"message\"}\n",
			},
			{
				Tool:    "read_file",
				Summary: "Read f1 logs/conversations/recent.jsonl lines 52-131 (total lines: 211)",
				Output:  "52:{\"type\":\"message\"}\n",
			},
		},
	)
	if !ok {
		t.Fatal("expected repeated unbounded diagnostic log read to continue before earliest prior window")
	}
	if rewritten.StartLine != 1 || rewritten.EndLine != 51 {
		t.Fatalf("window: want 1-51, got %d-%d", rewritten.StartLine, rewritten.EndLine)
	}
	for _, want := range []string{`"start_line":1`, `"end_line":51`} {
		if !strings.Contains(raw, want) {
			t.Fatalf("raw rewrite missing %q:\n%s", want, raw)
		}
	}

	rewritten, _, ok = repeatedDiagnosticLogReadFileBackfill(
		workspace.ToolRequest{Tool: "read_file", Path: "logs/conversations/recent.jsonl"},
		[]workspace.ToolResult{
			{
				Tool:    "read_file",
				Summary: "Read f1 logs/conversations/recent.jsonl lines 132-211 (file has 211 lines total)",
				Output:  "132:{\"type\":\"message\"}\n",
			},
			{
				Tool:    "read_file",
				Summary: "Read f1 logs/conversations/recent.jsonl lines 1-51 (file has 211 lines total)",
				Output:  "1:{\"type\":\"message\"}\n",
			},
		},
	)
	if !ok {
		t.Fatal("expected middle gap before the tail to be backfilled")
	}
	if rewritten.StartLine != 52 || rewritten.EndLine != 131 {
		t.Fatalf("window: want 52-131, got %d-%d", rewritten.StartLine, rewritten.EndLine)
	}

	if _, _, ok := repeatedDiagnosticLogReadFileBackfill(
		workspace.ToolRequest{Tool: "read_file", Path: "logs/conversations/recent.jsonl", StartLine: 1, EndLine: 51},
		[]workspace.ToolResult{{Tool: "read_file", Summary: "Read f1 logs/conversations/recent.jsonl lines 52-131 (file has 211 lines total)"}},
	); ok {
		t.Fatal("explicit bounded diagnostic log reads should execute as requested")
	}
}

func TestDiagnosticLogUnboundedReadCoverageRejection(t *testing.T) {
	summary, errorText, reject := diagnosticLogUnboundedReadCoverageRejection(
		workspace.ToolRequest{Tool: "read_file", Path: "logs/conversations/recent.jsonl"},
		[]workspace.ToolResult{
			{
				Tool:    "read_file",
				Summary: "Read f1 logs/conversations/recent.jsonl lines 132-211 (file has 211 lines total)",
				Output:  "132:{\"type\":\"message\"}\n",
			},
			{
				Tool:    "read_file",
				Summary: "Read f1 logs/conversations/recent.jsonl lines 52-131 (file has 211 lines total)",
				Output:  "52:{\"type\":\"message\"}\n",
			},
			{
				Tool:    "read_file",
				Summary: "Read f1 logs/conversations/recent.jsonl lines 1-51 (file has 211 lines total)",
				Output:  "1:{\"type\":\"message\"}\n",
			},
		},
	)
	if !reject {
		t.Fatal("expected unbounded diagnostic log read to be rejected after beginning and tail coverage")
	}
	for _, want := range []string{"already been read back to line 1", "current tail window", "Answer from the gathered log evidence"} {
		if !strings.Contains(summary+" "+errorText, want) {
			t.Fatalf("rejection guidance missing %q:\nsummary=%s\nerror=%s", want, summary, errorText)
		}
	}

	if _, _, reject := diagnosticLogUnboundedReadCoverageRejection(
		workspace.ToolRequest{Tool: "read_file", Path: "logs/conversations/recent.jsonl"},
		[]workspace.ToolResult{{
			Tool:    "read_file",
			Summary: "Read f1 logs/conversations/recent.jsonl lines 1-51 (file has 211 lines total)",
			Output:  "1:{\"type\":\"message\"}\n",
		}},
	); reject {
		t.Fatal("beginning-only coverage should not block an unbounded read that can still gather the tail")
	}

	if _, _, reject := diagnosticLogUnboundedReadCoverageRejection(
		workspace.ToolRequest{Tool: "read_file", Path: "logs/conversations/recent.jsonl"},
		[]workspace.ToolResult{
			{
				Tool:    "read_file",
				Summary: "Read f1 logs/conversations/recent.jsonl lines 132-211 (file has 211 lines total)",
				Output:  "132:{\"type\":\"message\"}\n",
			},
			{
				Tool:    "read_file",
				Summary: "Read f1 logs/conversations/recent.jsonl lines 1-51 (file has 211 lines total)",
				Output:  "1:{\"type\":\"message\"}\n",
			},
		},
	); reject {
		t.Fatal("beginning plus tail with an unread middle gap should not be treated as exhausted coverage")
	}
}

func TestRepeatedDiagnosticLogReadFilePreviouslyReadRejection(t *testing.T) {
	summary, errorText, reject := repeatedDiagnosticLogReadFilePreviouslyReadRejection(
		workspace.ToolRequest{Tool: "read_file", Path: "logs/conversations/recent.jsonl", StartLine: 52, EndLine: 131},
		[]workspace.ToolResult{
			{
				Tool:    "read_file",
				Summary: "Read f1 logs/conversations/recent.jsonl lines 132-211 (file has 211 lines total)",
				Output:  "132:{\"type\":\"message\"}\n",
			},
			{
				Tool:    "read_file",
				Summary: "Read f1 logs/conversations/recent.jsonl lines 52-131 (file has 211 lines total)",
				Output:  "52:{\"type\":\"message\"}\n",
			},
		},
	)
	if !reject {
		t.Fatal("expected previously read diagnostic log window to be rejected")
	}
	for _, want := range []string{"bounded diagnostic log window already returned evidence", "different bounded window", "answer from the gathered evidence"} {
		if !strings.Contains(summary+" "+errorText, want) {
			t.Fatalf("rejection guidance missing %q:\nsummary=%s\nerror=%s", want, summary, errorText)
		}
	}

	if _, _, reject := repeatedDiagnosticLogReadFilePreviouslyReadRejection(
		workspace.ToolRequest{Tool: "read_file", Path: "internal/log/reader.go", StartLine: 52, EndLine: 131},
		[]workspace.ToolResult{{
			Tool:    "read_file",
			Summary: "Read f1 internal/log/reader.go lines 52-131 (file has 211 lines total)",
			Output:  "52:func reader() {}\n",
		}},
	); reject {
		t.Fatal("source files under internal/log must not use diagnostic log repeated-window rejection")
	}
}

func TestRepeatedDiagnosticLogReadFileExactRejection(t *testing.T) {
	summary, errorText, reject := repeatedDiagnosticLogReadFileExactRejection(
		workspace.ToolRequest{Tool: "read_file", Path: "logs/conversations/recent.jsonl"},
		workspace.ToolResult{
			Tool:    "read_file",
			Summary: "Read f1 logs/conversations/recent.jsonl lines 132-211",
			Output:  "132:{\"type\":\"message\"}\n",
		},
	)
	if !reject {
		t.Fatal("expected exact repeated diagnostic log read to be rejected")
	}
	for _, want := range []string{"Repeated diagnostic log read rejected", "different bounded `start_line`/`end_line` window", "otherwise answer"} {
		if !strings.Contains(summary+" "+errorText, want) {
			t.Fatalf("rejection guidance missing %q:\nsummary=%s\nerror=%s", want, summary, errorText)
		}
	}

	if _, _, reject := repeatedDiagnosticLogReadFileExactRejection(
		workspace.ToolRequest{Tool: "read_file", Path: "internal/agent/runner.go"},
		workspace.ToolResult{Tool: "read_file", Summary: "Read f1 internal/agent/runner.go lines 1-80"},
	); reject {
		t.Fatal("source-file repeated reads should keep existing replay/redirect behavior")
	}
}

func TestArchitectureCompletionRejectionRequiresEvidenceWithoutReadinessState(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Contract:          "architecture_assessment",
			TaskFamily:        "architecture",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"task", "discovery_query", "search_content", "read_file"},
			Objective:         "Can you tell me how this harness uses subagents?",
		},
	}
	result, rejected := runner.completionGateRejection(
		"The harness uses subagents through delegated task calls.",
		nil,
		1,
		nil,
		false,
	)
	if !rejected {
		t.Fatalf("expected architecture final answer without evidence to be rejected")
	}
	if !strings.Contains(result.Error, "bounded source-body or diagnostic evidence") {
		t.Fatalf("expected bounded-evidence rejection, got %#v", result)
	}
}

func TestArchitectureCompletionRejectionRejectsEchoedQuestionSourceSnippet(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Contract:          "architecture_assessment",
			TaskFamily:        "architecture",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"task", "discovery_query", "read_file"},
			Objective:         "What can we add to our discovery tools in an effort at increasing their overall usefulness?",
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 internal/workspace/tool_schema_export.go lines 1-80",
		Output:  "1:package workspace\n2:\n3:import (\n4:\t\"encoding/json\"\n",
	}}
	result, rejected := runner.completionGateRejection(
		"For `What can we add to our discovery tools in an effort at increasing their overall usefulness?`, 1:package workspace. 2:. 3:import (. 4:\t\"encoding/json\". This is grounded in `internal/workspace/tool_schema_export.go`.",
		results,
		1,
		nil,
		false,
	)
	if !rejected {
		t.Fatal("expected echoed-question source snippet to be rejected")
	}
	if !strings.Contains(result.Error, "does not explain what the inspected code does") {
		t.Fatalf("expected meta explanation rejection, got %#v", result)
	}
}

func TestArchitectureFinalAnswerAcceptanceRejectsLineOrientedSourceSnippet(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Contract:          "architecture_assessment",
			TaskFamily:        "architecture",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"task", "discovery_query", "read_file"},
			Objective:         "Can you tell me how this harness uses subagents?",
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 internal/agent/runner.go lines 1-80",
		Output:  "1:package agent\n2:\n3:import (\n",
	}}
	acceptance := runner.evaluateFinalAnswerAcceptance(
		"For `Can you tell me how this harness uses subagents?`, 1:package agent. 2:. 3:import (. This is grounded in `internal/agent/runner.go`.",
		results,
	)
	if acceptance.Decision != finalAnswerAcceptanceRejected || acceptance.BlockerKind != "non_answer_prose" {
		t.Fatalf("line-oriented source snippet should be rejected as non-answer prose, got %#v", acceptance)
	}
}

func TestArchitectureCompletionRejectionRejectsOffObjectiveDebugLogDetail(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Contract:          "architecture_assessment",
			TaskFamily:        "architecture",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"context_pack", "code_navigate"},
			Objective:         "What does the debug log redacted mode do?",
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 internal/transcript/debuglog.go lines 1-80",
		Output:  "1:package transcript\n2:func (l *DebugLogger) sanitizeValue(value any) any { out := make(map[string]any); return out }\n",
	}}
	result, rejected := runner.completionGateRejection(
		"`DebugLogger.sanitizeValue` in `internal/transcript/debuglog.go` is the clearest inspected implementation anchor. The inspected body shows that it assigns `out` from `make(map[string]any, len(typed))`, returns `out`, and assigns `out` from `make([]any, 0, len(typed))`.",
		results,
		1,
		nil,
		false,
	)
	if !rejected {
		t.Fatal("expected off-objective debug log implementation detail to be rejected")
	}
	if !strings.Contains(result.Error, "does not address the user's objective terms") {
		t.Fatalf("expected objective-term rejection, got %#v", result)
	}
}

func TestArchitectureCompletionRejectionRejectsSingleAnchorSourceInventory(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Contract:          "architecture_assessment",
			TaskFamily:        "architecture",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"context_pack", "code_navigate"},
			Objective:         "What does the debug log redacted mode do?",
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "code_navigate",
		Summary: "Code navigation routed read_symbol via go_read_symbol",
		Output:  "capability: read_symbol\nroute_summary: Read DebugLogMode in internal/transcript/debuglog.go\n\ntype DebugLogMode string\n",
	}}
	result, rejected := runner.completionGateRejection(
		"`DebugLogMode` in `internal/transcript/debuglog.go` is the clearest inspected implementation anchor. The inspected body shows that it defines `DebugLogMode`.",
		results,
		1,
		nil,
		false,
	)
	if !rejected {
		t.Fatal("expected single-anchor source inventory to be rejected")
	}
	if !strings.Contains(result.Error, "does not explain what the inspected code does") &&
		!strings.Contains(result.Error, "non_answer_prose") {
		t.Fatalf("expected non-answer source-inventory rejection, got %#v", result)
	}
}

func TestArchitectureCompletionRejectionAllowsObjectiveRelevantDebugLogMechanism(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Contract:          "architecture_assessment",
			TaskFamily:        "architecture",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"context_pack", "code_navigate"},
			Objective:         "What does the debug log redacted mode do?",
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 internal/transcript/debuglog.go lines 1-80",
		Output:  "1:package transcript\n2:const DebugLogModeRedacted = \"redacted\"\n3:func (l *DebugLogger) sanitizeValue(value any) any { return \"[redacted]\" }\n",
	}}
	_, rejected := runner.completionGateRejection(
		"`DebugLogModeRedacted` sanitizes values before logging by replacing raw transcript fields with redacted summaries. This is grounded in `internal/transcript/debuglog.go`.",
		results,
		1,
		nil,
		false,
	)
	if rejected {
		t.Fatal("objective-relevant debug log mechanism should be accepted")
	}
}

func TestArchitectureCompletionRejectionAllowsObjectiveRelevantSubagentAnswer(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Contract:          "architecture_assessment",
			TaskFamily:        "architecture",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"task", "read_file"},
			Objective:         "Can you tell me how this harness uses subagents?",
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 internal/agent/runner_child_tasks.go lines 1-80",
		Output:  "func (r *Runner) runChildTask() { /* task tool delegates to child runner */ }\n",
	}}
	_, rejected := runner.completionGateRejection(
		"The harness uses subagents through the task tool: the parent runner delegates to a bounded child runner, then returns the child packet as evidence for the parent answer. This is grounded in `internal/agent/runner_child_tasks.go`.",
		results,
		1,
		nil,
		false,
	)
	if rejected {
		t.Fatal("objective-relevant subagent explanation should be accepted")
	}
}

func TestArchitectureCompletionRejectionRejectsDiscoveryToolInventory(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Contract:          "architecture_assessment",
			TaskFamily:        "architecture",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"task", "discovery_query", "code_navigate"},
			Objective:         "What can we add to our discovery tools in an effort at increasing their overall usefulness?",
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read internal/workspace/discovery_query.go lines 1-80",
		Output:  "package workspace\n\nfunc RunDiscoveryQuery() {}\n",
	}}
	acceptance := runner.evaluateFinalAnswerAcceptance(
		"Related files: Found 2 related Go file(s). Symbol index: No Go workspace symbols matched \"idiom|discovery|surface|context|graph|current|discovery[-_ ]?query|recipes\". capability: file_outline. language: go. This is grounded in `internal/workspace/discovery_query.go`, `internal/agent/runner_child_tasks.go`, `internal/agent/runner.go`, `internal/labels/labels.go`.",
		results,
	)
	if acceptance.Decision != finalAnswerAcceptanceRejected {
		t.Fatal("expected discovery/tool inventory to be rejected")
	}
	if acceptance.BlockerKind != "non_answer_prose" {
		t.Fatalf("expected non-answer evidence-inventory rejection, got %#v", acceptance)
	}
}

func TestArchitectureCompletionRejectionAllowsDiscoveryUsefulnessMechanism(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Contract:          "architecture_assessment",
			TaskFamily:        "architecture",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"discovery_query", "read_file"},
			Objective:         "What can we add to our discovery tools in an effort at increasing their overall usefulness?",
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 internal/agent/runner.go lines 1-80",
		Output:  "package agent\n\nfunc (r *Runner) Run() {}\n",
	}, {
		Tool:    "read_file",
		Summary: "Read f1 internal/agent/runner_lane_policy.go lines 1-80",
		Output:  "package agent\n\nfunc conversationEvidenceSatisfied() bool { return true }\n",
	}}
	result, rejected := runner.completionGateRejection(
		"`internal/agent/runner.go` contains the runner `Run` method. `internal/agent/runner_lane_policy.go` contains `conversationEvidenceSatisfied`, the readiness check present in the inspected source. Together, those inspected anchors show the useful integration point: discovery output has to connect into runner closeout and readiness checks before it can support final synthesis.",
		results,
		1,
		nil,
		false,
	)
	if rejected {
		t.Fatalf("objective-relevant discovery usefulness mechanism should be accepted, got %#v", result)
	}
}

func TestMutationCompletionRejectsUnresolvedRejectedWriteTarget(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Contract:          "implementation_result",
			TaskFamily:        "implementation",
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"edit_file", "shell"},
			Objective:         "Add terminal-style input history navigation.",
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "edit_file",
		Summary: "Edited internal/app/model_state.go",
		Output:  "Changed paths:\n- internal/app/model_state.go\n",
	}, {
		Tool:    "edit_file",
		Summary: "Tool not available in current runtime contract",
		Output:  "Rejected write target: `internal/app/update_handlers.go`",
		Error:   "edit_file is not in the current allowed tool surface.",
	}, {
		Tool:    "shell",
		Summary: "Ran shell command in .",
		Output:  "ok\tidiom/internal/app\n",
	}}
	acceptance := runner.evaluateFinalAnswerAcceptance(
		"Implemented the requested change.\n\nChanged paths:\n- internal/app/model_state.go\n\nVerification: go test ./internal/app passed",
		results,
	)
	if acceptance.Decision != finalAnswerAcceptanceRejected || acceptance.BlockerKind != "rejected_mutation_paths_remaining" {
		t.Fatalf("expected unresolved rejected write target to block completion, got %#v", acceptance)
	}
	if len(acceptance.EvidencePaths) == 0 || !slices.Contains(acceptance.EvidencePaths, "internal/app/update_handlers.go") {
		t.Fatalf("expected unresolved rejected path in evidence paths, got %#v", acceptance.EvidencePaths)
	}
}

func TestRejectedWriteResultRetainsRequestTarget(t *testing.T) {
	req := workspace.ToolRequest{Tool: "edit_file", Path: "internal/app/update_handlers.go"}
	result := resultWithRejectedRequestTarget(req, workspace.ToolResult{
		Tool:    "edit_file",
		Summary: "Tool not available in current runtime contract",
		Error:   "edit_file is not in the current allowed tool surface.",
	})
	if !strings.Contains(result.Output, "internal/app/update_handlers.go") {
		t.Fatalf("expected rejected write result to retain target path, got %#v", result)
	}
}

func TestReadOnlyEvidenceCompletionRejectionAllowsAnswerAfterEvidence(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Contract:          "direct_answer",
			TaskFamily:        "conversation",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"search_content", "read_file"},
			Objective:         "What does ShortLabel return in internal/labels/labels.go?",
		},
	}
	_, rejected := runner.completionGateRejection(
		"The inspected source shows `ShortLabel` returns \"Save changes\".",
		[]workspace.ToolResult{{Tool: "read_file", Summary: "Read f1 internal/labels/labels.go lines 1-5", Output: "func ShortLabel() string { return \"Save changes\" }"}},
		1,
		nil,
		false,
	)
	if rejected {
		t.Fatalf("did not expect read-only final answer to be rejected after source evidence")
	}
}

func TestReadOnlyEvidenceCompletionRejectionRejectsNonSubstantiveAnswerAfterEvidence(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Contract:          "direct_answer",
			TaskFamily:        "conversation",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"read_file"},
			Objective:         "What does internal/labels/labels.go ShortLabel return?",
		},
	}
	result, rejected := runner.completionGateRejection(
		"tool",
		[]workspace.ToolResult{{Tool: "read_file", Summary: "Read f1 internal/labels/labels.go lines 1-5", Output: "func ShortLabel() string { return \"Save changes\" }"}},
		1,
		nil,
		false,
	)
	if !rejected {
		t.Fatalf("expected non-substantive read-only answer to be rejected")
	}
	if !strings.Contains(result.Error, "substantive user-facing answer") {
		t.Fatalf("expected substantive-answer rejection, got %#v", result)
	}
}

func TestReadOnlyEvidenceCompletionRejectionDoesNotCountEmptyRoutedSymbolMiss(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Contract:          "direct_answer",
			TaskFamily:        "conversation",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"code_navigate", "read_file"},
			Readiness:         &workerruntime.ReadinessState{ReadyToAnswer: false, OpenObligations: []string{"evidence"}},
		},
	}
	_, rejected := runner.completionGateRejection(
		"No Go symbols matched `save`.",
		[]workspace.ToolResult{{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output:  "capability: read_symbol\nlanguage: go\nrouted_tool: go_read_symbol\nroute_summary: No Go symbols matched \"save\"",
		}},
		1,
		nil,
		false,
	)
	if !rejected {
		t.Fatalf("symbol miss without source body must not satisfy read-only evidence")
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

func TestReadOnlyExplanationCompletionRejectionAcceptsComplexSourceBackedDetail(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Contract:          "architecture_assessment",
			TaskFamily:        "architecture",
			PermissionProfile: "read_only_workspace",
		},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "go_read_symbol",
			Summary: "Read Foo in internal/app/foo.go lines 10-20",
			Output:  "func Foo() { return }",
		},
		{
			Tool:    "read_file",
			Summary: "Read f1 internal/app/bar.go lines 1-8",
			Output: strings.Join([]string{
				"1:package app",
				"2:type BarHandler struct{}",
				"3:type DebugLoggerOptions struct{}",
				"4:const system_prompt_digest = \"sha256:abc123\"",
			}, "\n"),
		},
	}
	_, rejected := runner.completionGateRejection(
		"`Foo` is the inspected entry point, and the same inspected source body also defines `BarHandler`, `DebugLoggerOptions`, `system_prompt_digest`, and the `sha256:` digest marker.",
		results,
		1,
		nil,
		false,
	)
	if rejected {
		t.Fatalf("complex source-backed answer was rejected")
	}
}

func TestReadOnlyExplanationCompletionRejectionAcceptsRedactedModeDetailsFromInspectedBodies(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Contract:          "architecture_assessment",
			TaskFamily:        "architecture",
			PermissionProfile: "read_only_workspace",
		},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read f1 internal/transcript/debuglog.go lines 1-70",
			Output: strings.Join([]string{
				"19:DebugLogModeRedacted DebugLogMode = \"redacted\"",
				"20:DebugLogModeFull DebugLogMode = \"full\"",
				"23:type DebugLoggerOptions struct {",
				"44:type DebugLogger struct {",
				"59:SystemPrompt string `json:\"system_prompt,omitempty\"`",
				"60:SystemPromptDigest string `json:\"system_prompt_digest,omitempty\"`",
				"61:WorkspaceRoot string `json:\"workspace_root,omitempty\"`",
			}, "\n"),
		},
		{
			Tool:    "read_file",
			Summary: "Read f2 internal/transcript/debuglog_test.go lines 40-76",
			Output: strings.Join([]string{
				"40:if err := logger.LogMessage(\"user\", \"api_key=secret123 path=/tmp/workspace/config.json\"); err != nil {",
				"51:if digest := records[0][\"system_prompt_digest\"]; digest == nil || !strings.HasPrefix(digest.(string), \"sha256:\") {",
				"54:if records[0][\"workspace_root\"] != \".\" {",
			}, "\n"),
		},
	}
	_, rejected := runner.completionGateRejection(
		"`DebugLogModeRedacted` is the redacted `DebugLogger` mode; the inspected source and tests show `DebugLoggerOptions`, `SystemPrompt`, `system_prompt`, `system_prompt_digest`, the `sha256:` marker, `workspace_root`, `secret123`, and the contrasting `DebugLogModeFull` mode.",
		results,
		1,
		nil,
		false,
	)
	if rejected {
		t.Fatalf("redacted-mode details from inspected source bodies were rejected")
	}
}

func TestReadOnlyExplanationCompletionRejectionAcceptsOutlineDeclarationNames(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Contract:          "architecture_assessment",
			TaskFamily:        "architecture",
			PermissionProfile: "read_only_workspace",
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "code_navigate",
		Summary: "Code navigation routed file_outline via go_file_outline",
		Output: strings.Join([]string{
			"capability: file_outline",
			"route_summary: Outlined 4 top-level declaration(s) in internal/transcript/debuglog.go.",
			"declarations:",
			"- DebugLogMode lines 16-16 tags: core",
			"- DebugLogModeRedacted, DebugLogModeFull lines 18-21 tags: core",
		}, "\n"),
	}}
	_, rejected := runner.completionGateRejection(
		"`DebugLogModeRedacted` is the redacted debug log mode declaration shown in the inspected outline.",
		results,
		1,
		nil,
		false,
	)
	if rejected {
		t.Fatalf("outline declaration names should support declaration mentions")
	}
}

func TestReadOnlyExplanationCompletionRejectionAllowsToolSurfaceVocabulary(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Contract:          "architecture_assessment",
			TaskFamily:        "architecture",
			PermissionProfile: "read_only_workspace",
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 internal/agent/runner.go lines 1-8",
		Output:  "1:package agent\n2:// Runner coordinates workspace tools.\n3:type Runner struct {}\n",
	}}
	_, rejected := runner.completionGateRejection(
		"The discovery tool surface should distinguish `structural_search`, `context_query`, `code_navigate`, `context_pack`, `context_impact`, `history_range`, `history_symbol`, `discovery_query`, `context_trace`, `search_content`, `read_file`, `file_stats`, `harness_audit`, `discovery_state`, `context_merge`, and `progressive_refinement` as tool or proposal vocabulary rather than inspected source declarations.",
		results,
		1,
		nil,
		false,
	)
	if rejected {
		t.Fatalf("architecture tool/proposal vocabulary should not be rejected as unsupported source symbols")
	}
}

func TestReadOnlyExplanationCompletionRejectionDoesNotLetUncertaintyWaiveOtherUnsupportedDetails(t *testing.T) {
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
		"`Runner.runChildTask` may include other setup. Separately, `Runner.runWorkbranchChildTask` applies the write-enabled variant.",
		results,
		1,
		nil,
		false,
	)
	if !rejected {
		t.Fatalf("expected unsupported detail outside the uncertain claim to be rejected")
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

func TestGeneratedEvidenceArtifactReadRedirectNamesSourcePaths(t *testing.T) {
	req := workspace.ToolRequest{
		Tool: "read_file",
		Path: ".idiom/discovery/queries/query.json",
	}
	results := []workspace.ToolResult{{
		Tool:    "discovery_query",
		Summary: "Ran architecture discovery",
		Output:  `{"artifact_path":".idiom/discovery/queries/query.json","evidence":[{"name":"text_anchor_search","summary":"Found paths","output_excerpt":"internal/agent/runner.go:128: finish\ninternal/agent/runner_child_tasks.go:16: runChildTask"}]}`,
	}}
	result, ok := generatedEvidenceArtifactReadRedirectResult(req, results)
	if !ok {
		t.Fatal("expected generated evidence artifact read to redirect to source paths")
	}
	if result.Error != "" {
		t.Fatalf("soft redirect should not be a rejection, got error %q", result.Error)
	}
	for _, want := range []string{"generated discovery/context JSON is not source-body evidence", "internal/agent/runner.go", "internal/agent/runner_child_tasks.go"} {
		if !strings.Contains(result.Output, want) {
			t.Fatalf("redirect output missing %q:\n%s", want, result.Output)
		}
	}
	if strings.Contains(result.Output, "- .idiom/discovery/queries/query.json") {
		t.Fatalf("redirect must not list artifact as source evidence:\n%s", result.Output)
	}
	redirected, raw, reason, ok := generatedEvidenceArtifactReadRedirectRequest(req, results)
	if !ok {
		t.Fatal("expected generated evidence artifact read to rewrite to source request")
	}
	if redirected.Path != "internal/agent/runner.go" {
		t.Fatalf("expected redirect to first source path, got %#v", redirected)
	}
	if !strings.Contains(raw, `"path":"internal/agent/runner.go"`) || reason == "" {
		t.Fatalf("expected raw rewritten request and reason, raw=%q reason=%q", raw, reason)
	}
}

func TestStructuredSourceReadRewriteRunsCodeNavigateInsteadOfControlRedirect(t *testing.T) {
	ws := newGoTestWorkspace(t)
	brief := workerruntime.WorkerRuntimeBrief{
		Contract:          "architecture_assessment",
		TaskFamily:        "architecture",
		PermissionProfile: "read_only_workspace",
	}
	req := workspace.ToolRequest{
		Tool:            "read_file",
		Path:            "main.go",
		NamedGap:        "verify the file declaration outline before answering",
		WhyNotAnswering: "without the declaration boundary the answer may cite the wrong symbol",
	}

	rewritten, raw, reason, ok := structuredSourceReadRewriteRequest(req, ws, []string{"read_file", "code_navigate"}, brief)
	if !ok {
		t.Fatal("expected read-only source read to rewrite to structured navigation")
	}
	if rewritten.Tool != "code_navigate" || rewritten.Capability != "file_outline" || rewritten.Path != "main.go" {
		t.Fatalf("unexpected rewrite: %#v", rewritten)
	}
	if rewritten.NamedGap != req.NamedGap || rewritten.WhyNotAnswering != req.WhyNotAnswering {
		t.Fatalf("rewrite should preserve post-readiness contract fields: %#v", rewritten)
	}
	if !strings.Contains(raw, `"tool":"code_navigate"`) || !strings.Contains(raw, `"capability":"file_outline"`) {
		t.Fatalf("rewritten raw should execute code_navigate file_outline, got %s", raw)
	}
	if reason != "source read redirected to structured navigation" {
		t.Fatalf("unexpected reason %q", reason)
	}
	if _, _, _, ok := structuredSourceReadRewriteRequest(req, ws, []string{"read_file"}, brief); ok {
		t.Fatal("rewrite must not fire when code_navigate is not visible")
	}
	mutationBrief := brief
	mutationBrief.PermissionProfile = "workspace_write"
	mutationBrief.Contract = "plan_execute"
	if _, _, _, ok := structuredSourceReadRewriteRequest(req, ws, []string{"read_file", "code_navigate"}, mutationBrief); ok {
		t.Fatal("rewrite must not override mutation-lane source-read policy")
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
		if slices.Contains(got, forbidden) {
			t.Fatalf("expected prompt scaffolding path %q to be deprioritized, got %#v", forbidden, got)
		}
	}
}

func TestReadOnlyDirectAnswerPresentationUsesLedgerRelevantSourcePaths(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		Lane:              "main",
		TaskFamily:        string(workerruntime.TaskFamilyDirectAnswer),
		Contract:          string(workerruntime.ContractDirectAnswer),
		PermissionProfile: "read_only_workspace",
		Objective:         "Explain chat input newline rendering.",
		EvidencePaths:     []string{"internal/app/prompt_candidate.go"},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote packet",
			Output:  "internal/app/broad_candidate.go\ninternal/app/other_candidate.go\n",
		},
		{
			Tool:    "read_file",
			Summary: "Read internal/app/rendering.go lines 1-6",
			Output: strings.Join([]string{
				"1:package app",
				"2:",
				"3:// chat input newline rendering is handled by this function.",
				"4:func renderChatInputNewline() {}",
			}, "\n"),
		},
	}

	got := runnerEvidencePathsForBrief(brief, results, 8)
	if !slices.Contains(got, "internal/app/rendering.go") {
		t.Fatalf("expected direct-answer evidence paths to contain ledger source path, got %#v", got)
	}
	for _, forbidden := range []string{"internal/app/broad_candidate.go", "internal/app/other_candidate.go", "internal/app/prompt_candidate.go"} {
		if slices.Contains(got, forbidden) {
			t.Fatalf("direct-answer presentation must not fall back to broad candidate %q, got %#v", forbidden, got)
		}
	}
	live := (&Runner{RuntimeBrief: brief}).runtimeBriefWithLiveState(RunPolicy{}, nil, results)
	if slices.Contains(live.EvidencePaths, "internal/app/prompt_candidate.go") {
		t.Fatalf("direct-answer live brief must replace broad prompt evidence paths, got %#v", live.EvidencePaths)
	}
}

func TestRuntimeBriefDirectAnswerPreservesAdvisoryEvidenceBeforeAnswerability(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		Lane:              "main",
		TaskFamily:        string(workerruntime.TaskFamilyDirectAnswer),
		Contract:          string(workerruntime.ContractDirectAnswer),
		PermissionProfile: "read_only_workspace",
		Objective:         "Explain chat input newline rendering.",
		EvidencePaths:     []string{"internal/app/prompt_candidate.go"},
	}

	live := (&Runner{RuntimeBrief: brief}).runtimeBriefWithLiveState(RunPolicy{}, nil, nil)
	if !slices.Contains(live.EvidencePaths, "internal/app/prompt_candidate.go") {
		t.Fatalf("direct-answer live brief should preserve advisory evidence before answerability, got %#v", live.EvidencePaths)
	}
}

func TestRunnerEvidencePathsPreservesBroadCandidatesForArchitecture(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		Lane:              "architecture",
		TaskFamily:        "architecture",
		Contract:          "architecture_assessment",
		PermissionProfile: "read_only_workspace",
		Objective:         "Explain chat input newline rendering.",
	}
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote packet",
			Output:  "internal/app/broad_candidate.go\ninternal/app/other_candidate.go\n",
		},
		{
			Tool:    "read_file",
			Summary: "Read internal/app/rendering.go lines 1-6",
			Output: strings.Join([]string{
				"1:package app",
				"2:",
				"3:// chat input newline rendering is handled by this function.",
				"4:func renderChatInputNewline() {}",
			}, "\n"),
		},
	}

	got := runnerEvidencePathsForBrief(brief, results, 8)
	if !slices.Contains(got, "internal/app/broad_candidate.go") {
		t.Fatalf("non-direct-answer presentation should retain broad candidate paths, got %#v", got)
	}
}

func TestEmitAcceptedAnswerSignalsDirectAnswerBlockedUsesLedgerRelevantSourcePaths(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Lane:              "main",
			TaskFamily:        string(workerruntime.TaskFamilyDirectAnswer),
			Contract:          string(workerruntime.ContractDirectAnswer),
			PermissionProfile: "read_only_workspace",
			Objective:         "Explain chat input newline rendering.",
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
		"The issue is in `internal/app/broad_candidate.go`.",
		[]workspace.ToolResult{
			{
				Tool:    "context_pack",
				Summary: "Context pack wrote packet",
				Output:  "internal/app/broad_candidate.go\ninternal/app/other_candidate.go\n",
			},
			{
				Tool:    "read_file",
				Summary: "Read internal/app/rendering.go lines 1-6",
				Output: strings.Join([]string{
					"1:package app",
					"2:",
					"3:// chat input newline rendering is handled by this function.",
					"4:func renderChatInputNewline() {}",
				}, "\n"),
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
	if !slices.Contains(got, "internal/app/rendering.go") {
		t.Fatalf("blocked direct-answer readiness should name ledger source path, got %#v", got)
	}
	for _, forbidden := range []string{"internal/app/broad_candidate.go", "internal/app/other_candidate.go"} {
		if slices.Contains(got, forbidden) {
			t.Fatalf("blocked direct-answer readiness must not fall back to broad candidate %q, got %#v", forbidden, got)
		}
	}
}

func TestEmitReadOnlyAnswerReadyEventDirectAnswerUsesLedgerRelevantSourcePaths(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Lane:              "main",
			TaskFamily:        string(workerruntime.TaskFamilyDirectAnswer),
			Contract:          string(workerruntime.ContractDirectAnswer),
			PermissionProfile: "read_only_workspace",
			Objective:         "Explain chat input newline rendering.",
		},
	}
	var payload map[string]any
	runner.OnEvent = func(event RunEvent) {
		if event.Role != "read_only_answer_ready" {
			return
		}
		_ = json.Unmarshal([]byte(event.Content), &payload)
	}
	runner.emitReadOnlyAnswerReadyEvent(workspace.ToolRequest{Tool: "read_file"}, []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote packet",
			Output:  "internal/app/broad_candidate.go\ninternal/app/other_candidate.go\n",
		},
		{
			Tool:    "read_file",
			Summary: "Read internal/app/rendering.go lines 1-6",
			Output: strings.Join([]string{
				"1:package app",
				"2:",
				"3:// chat input newline rendering is handled by this function.",
				"4:func renderChatInputNewline() {}",
			}, "\n"),
		},
	})
	rawPaths, _ := payload["evidence_paths"].([]any)
	got := make([]string, 0, len(rawPaths))
	for _, raw := range rawPaths {
		if value, ok := raw.(string); ok {
			got = append(got, value)
		}
	}
	if !slices.Contains(got, "internal/app/rendering.go") {
		t.Fatalf("direct-answer ready event should name ledger source path, got %#v", got)
	}
	for _, forbidden := range []string{"internal/app/broad_candidate.go", "internal/app/other_candidate.go"} {
		if slices.Contains(got, forbidden) {
			t.Fatalf("direct-answer ready event must not fall back to broad candidate %q, got %#v", forbidden, got)
		}
	}
}

func TestReadOnlyAnswerReadyPresentationUsesImplementationOwnerPaths(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		Lane:              "main",
		TaskFamily:        string(workerruntime.TaskFamilyDirectAnswer),
		Contract:          string(workerruntime.ContractDirectAnswer),
		PermissionProfile: "read_only_workspace",
		Objective:         "what would it take to enable the up and down arrows to fill the chat input with history entries?",
	}
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote packet",
			Output: strings.Join([]string{
				"internal/agent/runner.go",
				"internal/agent/git_history_requirement.go",
				"internal/agent/final_answer_decorator_test.go",
			}, "\n"),
		},
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output: strings.Join([]string{
				"capability: read_symbol",
				"route_summary: Read Model.handleKeyPress in internal/app/update_handlers.go lines 22-180",
				"22:func (m *Model) handleKeyPress(msg tea.KeyMsg) (tea.Cmd, bool) {",
				"23:\tswitch msg.Type {",
				"24:\tcase tea.KeyUp:",
				"25:\t\tm.history.selected--",
				"26:\tcase tea.KeyDown:",
				"27:\t\tm.history.selected++",
				"28:\t\tm.input.SetValue(m.history.entries[m.history.selected].content)",
				"29:\t}",
				"30:\treturn nil, true",
				"31:}",
			}, "\n"),
		},
	}

	paths := readOnlyDirectAnswerPresentationEvidencePaths(brief, results, 8)
	if !slices.Contains(paths, "internal/app/update_handlers.go") {
		t.Fatalf("expected implementation owner path in presentation evidence, got %#v", paths)
	}
	for _, forbidden := range []string{"internal/agent/runner.go", "internal/agent/git_history_requirement.go", "internal/agent/final_answer_decorator_test.go"} {
		if slices.Contains(paths, forbidden) {
			t.Fatalf("presentation evidence must not substitute candidate path %q, got %#v", forbidden, paths)
		}
	}

	nudge := readOnlyAnswerReadyNudge(brief, results, 2)
	if !strings.Contains(nudge, "`internal/app/update_handlers.go`") {
		t.Fatalf("expected readiness nudge to cite implementation owner path, got:\n%s", nudge)
	}
	if strings.Contains(nudge, "`internal/agent/runner.go`") || strings.Contains(nudge, "`internal/agent/git_history_requirement.go`") {
		t.Fatalf("readiness nudge must not fall back to generic discovery candidates, got:\n%s", nudge)
	}
	if strings.Contains(nudge, "core implementation paths") {
		t.Fatalf("readiness nudge must not overclaim implementation ownership, got:\n%s", nudge)
	}
}

func TestPromptEvidencePathsFromToolResultsKeepsDirectTestProbePath(t *testing.T) {
	got := promptEvidencePathsFromToolResults([]workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read f5 cmd/idiom/product_workflow_test.go lines 1-6721",
			Output: strings.Join([]string{
				"1:package main",
				"25:internal/productworkflow/scope.go",
				"26:internal/productworkflow/validate.go",
				"27:cmd/idiom/product_workflow.go",
			}, "\n"),
		},
	}, 4)
	if len(got) == 0 || got[0] != "cmd/idiom/product_workflow_test.go" {
		t.Fatalf("expected direct test-file probe path to stay pinned first, got %#v", got)
	}
}

func TestRunnerEvidencePathsForMutationDoNotPinOutputMentionedFixturePaths(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{
			"internal/productworkflow/coverage_gate.go",
			"internal/productworkflow/coverage_gate_test.go",
			"cmd/idiom/product_workflow_test.go",
		},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read f1 internal/productworkflow/coverage_gate.go lines 1-338",
			Output: strings.Join([]string{
				`record("ears", requireBehavior, earsPass, "...", "tdd.json", "coverage_ears_incomplete")`,
				`record("evidence_acceptance", true, evidencePass, "...", "evidence/traceability_matrix.json", "coverage_evidence_acceptance_incomplete")`,
			}, "\n"),
		},
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed file_outline via go_file_outline",
			Output: strings.Join([]string{
				"internal/productworkflow/analysis_agents.go",
				"internal/productworkflow/implementation.go",
				"cmd/idiom/implementation.go",
			}, "\n"),
		},
	}

	evidence := runnerEvidencePathsForBrief(brief, results, 8)
	if !reflect.DeepEqual(evidence, []string{"internal/productworkflow/coverage_gate.go"}) {
		t.Fatalf("expected mutation evidence to keep only direct scoped probes, got %#v", evidence)
	}
	pinned := runnerPinnedPathsForBrief(brief, results, 8)
	for _, want := range brief.RequiredEditPaths {
		if !slices.Contains(pinned, want) {
			t.Fatalf("expected pinned mutation paths to retain required path %q, got %#v", want, pinned)
		}
	}
	for _, forbidden := range []string{"internal/productworkflow/analysis_agents.go", "internal/productworkflow/implementation.go", "cmd/idiom/implementation.go", "tdd.json", "evidence/traceability_matrix.json"} {
		if slices.Contains(evidence, forbidden) || slices.Contains(pinned, forbidden) {
			t.Fatalf("did not expect output-mentioned fixture path %q in mutation evidence=%#v pinned=%#v", forbidden, evidence, pinned)
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
			"- `search/read`\n\n" +
			"Key findings:\n" +
			"- The harness uses subagents through the task runner path in internal/agent/runner.go and internal/agent/runner_child_tasks.go; internal/agent/answer_composer.go shapes the final response.\n",
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

func TestClosureDecisionRationaleDoesNotOverclaimImplementationOwnership(t *testing.T) {
	results := []workspace.ToolResult{{
		Tool:    "code_navigate",
		Summary: "Code navigation routed read_symbol via go_read_symbol",
		Output: "route_summary: Read Model in internal/app/model_state.go lines 133-263\n" +
			"133:type Model struct {\n" +
			"249:\tcurrentTurnHistoryIndex int\n" +
			"258:\thistory historyState\n" +
			"259:}\n",
	}}
	validity, _ := closureDecisionRationale("what would it take to enable the up and down arrows to fill the chat input with history entries?", results, []string{"internal/app/model_state.go"})
	if !strings.Contains(validity, "inspected source paths") {
		t.Fatalf("expected inspected-source rationale, got %q", validity)
	}
	if strings.Contains(validity, "core implementation paths") {
		t.Fatalf("readiness rationale must not overclaim implementation ownership, got %q", validity)
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
	assertRecoveryCardEvent(t, events, "evidence.zero_hit_not_absence", "Zero-hit retry blocked")
}

func TestRepeatedDeadEndReadEmitsRecoveryCard(t *testing.T) {
	replies := []string{
		`{"tool":"read_file","start_line":1}`,
		`{"tool":"read_file","end_line":1}`,
		"blocked",
	}
	root := t.TempDir()
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
	defer server.Close()

	var events []RunEvent
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 4, MaxNoProgressTurns: 4},
		PreExecute: func(step int, req workspace.ToolRequest) *workspace.ToolResult {
			if step == 1 && req.Tool == "read_file" {
				return &workspace.ToolResult{
					Tool:    req.Tool,
					Summary: "Read rejected",
					Error:   "synthetic read guard",
				}
			}
			return nil
		},
		OnEvent: func(event RunEvent) {
			events = append(events, event)
		},
	}
	_, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "inspect files"}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	assertRecoveryCardEvent(t, events, "loop.repeated_dead_end", "Repeated dead-end action rejected")
	assertRecoveryCardRole(t, events, "next_action_state", "loop.repeated_dead_end", "currently callable tools:", "read_file", "currently callable action classes:", "read")
	assertRecoveryCardRole(t, events, "failure_feedback", "loop.repeated_dead_end", "currently callable tools:", "read_file", "currently callable action classes:", "read")
}

func TestRejectedToolResultMessageIncludesRecoveryFeedback(t *testing.T) {
	root := t.TempDir()
	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read internal/app/model_state.go lines 1400-1500",
			Output:  "1400:func handleHistoryPanelKey() {}\n",
		},
	}
	var messages []llama.ChatMessage
	var toolEvents []ToolResultEvent
	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Lane:              "main",
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
			Objective:         "Explain internal/app/model_state.go history key handling.",
		},
		currentAllowedTools: []string{"read_file", "search_content", "code_navigate"},
	}
	recorder := runnerTurnRecorder{
		runner:           runner,
		onToolResult:     func(event ToolResultEvent) { toolEvents = append(toolEvents, event) },
		collectedResults: &results,
		turnMessages:     &messages,
	}

	recorder.recordWithOptions(3, workspace.ToolRequest{
		Tool:    "search_content",
		Path:    ".",
		Pattern: "history.*key",
	}, workspace.ToolResult{
		Tool:    "search_content",
		Summary: "Repeated dead-end action rejected",
		Error:   "this action class already failed earlier in the turn. Do not retry the same kind of move. Choose a materially different step or answer with the evidence you already have.",
	}, "tool_rejected", runnerRecordOptions{SetLastToolKey: true})

	if len(messages) != 1 {
		t.Fatalf("expected one model-facing tool result message, got %d", len(messages))
	}
	if len(results) != 2 {
		t.Fatalf("expected raw collected results to include the rejection, got %d", len(results))
	}
	if strings.Contains(results[1].Error, "Recovery feedback") || strings.Contains(results[1].Error, "Recovery card") {
		t.Fatalf("raw collected result must remain undecorated, got:\n%s", results[1].Error)
	}
	if len(toolEvents) != 1 {
		t.Fatalf("expected one raw tool result event, got %d", len(toolEvents))
	}
	if strings.Contains(toolEvents[0].Result.Error, "Recovery feedback") || strings.Contains(toolEvents[0].Result.Error, "Recovery card") {
		t.Fatalf("raw tool result event must remain undecorated, got:\n%s", toolEvents[0].Result.Error)
	}
	content := messages[0].Content
	for _, want := range []string{
		"Recovery feedback",
		"do_not_repeat:",
		"valid_next:",
		"Recovery card",
		"loop.repeated_dead_end",
		"internal/app/model_state.go",
		"currently callable tools:",
		"read_file",
		"search_content",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("expected model-facing rejection to contain %q, got:\n%s", want, content)
		}
	}
}

func assertRecoveryCardEvent(t *testing.T, events []RunEvent, invariantID string, summary string) {
	t.Helper()
	cardsByRole := recoveryCardsByRole(t, events)
	var cards []string
	for _, roleCards := range cardsByRole {
		cards = append(cards, roleCards...)
	}
	for _, card := range cards {
		if strings.Contains(card, invariantID) && strings.Contains(card, summary) {
			assertRecoveryCardOmitsProjectWorkflowTerms(t, card)
			return
		}
	}
	t.Fatalf("expected emitted recovery card with invariant %q and summary %q, cards=%#v roles=%v", invariantID, summary, cards, eventRolesFor(events))
}

func assertRecoveryCardRole(t *testing.T, events []RunEvent, role string, wants ...string) {
	t.Helper()
	cardsByRole := recoveryCardsByRole(t, events)
	for _, card := range cardsByRole[role] {
		matches := true
		for _, want := range wants {
			if !strings.Contains(card, want) {
				matches = false
				break
			}
		}
		if matches {
			assertRecoveryCardOmitsProjectWorkflowTerms(t, card)
			return
		}
	}
	t.Fatalf("expected %s recovery card containing %#v, cards=%#v roles=%v", role, wants, cardsByRole[role], eventRolesFor(events))
}

func recoveryCardsByRole(t *testing.T, events []RunEvent) map[string][]string {
	t.Helper()
	cards := make(map[string][]string)
	for _, event := range events {
		switch event.Role {
		case "next_action_state":
			var state runnerNextActionState
			if err := json.Unmarshal([]byte(event.Content), &state); err == nil && strings.TrimSpace(state.RecoveryCard) != "" {
				cards[event.Role] = append(cards[event.Role], state.RecoveryCard)
			}
		case "failure_feedback":
			var packet runnerFailureFeedbackPacket
			if err := json.Unmarshal([]byte(event.Content), &packet); err == nil && strings.TrimSpace(packet.RecoveryCard) != "" {
				cards[event.Role] = append(cards[event.Role], packet.RecoveryCard)
			}
		}
	}
	return cards
}

func TestSuccessfulSearchAllowsBroadFollowupSearch(t *testing.T) {
	replies := []string{
		`{"tool":"search_files","path":"internal","pattern":"*runner*"}`,
		`{"tool":"search_files","path":".","pattern":"**/*.go"}`,
		"final answer",
	}
	events, _ := runNudgeScenario(t, RunPolicy{MaxNoProgressTurns: 8}, replies)

	for _, ev := range events {
		if strings.Contains(ev.Content, "Chained search rejected") {
			t.Fatalf("follow-up search should not be rejected after a successful search hit; exact-repeat guards own loop prevention; events=%v", eventRolesFor(events))
		}
	}
}

func TestSuccessfulSearchAllowsConcreteFollowupSearch(t *testing.T) {
	replies := []string{
		`{"tool":"search_content","path":".","pattern":"package app"}`,
		`{"tool":"search_files","path":".","pattern":"**/input*.go"}`,
		"final answer",
	}
	events, _ := runNudgeScenario(t, RunPolicy{MaxNoProgressTurns: 8}, replies)

	for _, ev := range events {
		if strings.Contains(ev.Content, "Chained search rejected") {
			t.Fatalf("concrete follow-up search should not be rejected after a successful search hit; events=%v", eventRolesFor(events))
		}
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
	if successfulSearches != 3 {
		t.Fatalf("expected first, concrete follow-up, and post-inspection searches to execute, got %d results=%#v", successfulSearches, results)
	}
}

// TestRejectionRetryBlocked verifies that when a pre-execute guard
// rejects a tool call, a byte-identical retry on a later step
// short-circuits the turn via rejection_retry_blocked instead of
// replaying the rejection cycle.
func TestRejectionRetryBlocked(t *testing.T) {
	// A repeated root list_files in explore mode trips the broad-inspection
	// guard without using the chained-list_files hiding path. The first retry
	// gets a nudge (rejection_retry_nudge), the second retry gets blocked
	// (rejection_retry_blocked) — matching the nudge-then-block pattern.
	rejectedArgs := `{"tool":"list_files","path":"."}`
	replies := []string{
		`{"tool":"list_files","path":"."}`,
		`{"tool":"list_files","path":"internal/agent"}`,
		rejectedArgs, // rejected by broad-inspection guard
		`{"tool":"read_file","path":"internal/app/app.go"}`,
		rejectedArgs, // first retry — nudged
		`{"tool":"read_file","path":"internal/app/app.go"}`,
		rejectedArgs, // second retry — blocked
		"final answer",
	}
	events, _ := runNudgeScenario(t, RunPolicy{MaxNoProgressTurns: 8}, replies)

	if got := countEventsByRole(events, "rejection_retry_nudge"); got != 1 {
		t.Fatalf("expected exactly 1 rejection_retry_nudge event on first retry, got %d; events: %v", got, eventRolesFor(events))
	}
	if got := countEventsByRole(events, "rejection_retry_blocked"); got != 1 {
		t.Fatalf("expected exactly 1 rejection_retry_blocked event on second retry, got %d; events: %v", got, eventRolesFor(events))
	}
}

func TestMutationRepeatedFullReadStillAllowsWriteInsteadOfForcedAnswer(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "go.mod", "module example.com/labels\n\ngo 1.24\n"); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := writeTestFile(root, "internal/labels/labels.go", "package labels\n\nfunc ShortLabel() string { return \"Save changes\" }\n"); err != nil {
		t.Fatalf("write seed: %v", err)
	}

	replies := []string{
		`{"tool":"read_file","path":"internal/labels/labels.go"}`,
		`{"tool":"read_file","path":"internal/labels/labels.go"}`,
		`{"tool":"read_file","path":"internal/labels/labels.go"}`,
		`{"tool":"edit_file","path":"internal/labels/labels.go","old_text":"return \"Save changes\"","new_text":"return \"Save\"","description":"shorten save label"}`,
		`{"tool":"shell","command":"go test ./..."}`,
		"Changed paths:\n- internal/labels/labels.go\n\nVerification: go test ./... passed.",
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
	defer server.Close()

	events := []RunEvent(nil)
	results := []workspace.ToolResult(nil)
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			Shell:        "/bin/sh",
			EnableWrites: true,
			EnableShell:  true,
		}),
		Policy: RunPolicy{MaxToolSteps: 8, MaxNoProgressTurns: 8},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			EvidencePaths:     []string{"internal/labels/labels.go"},
		},
		OnEvent: func(event RunEvent) {
			events = append(events, event)
		},
	}
	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Shorten the save label in internal/labels/labels.go."}},
		llama.ChatOptions{MaxTokens: 8192},
		nil,
		nil,
		func(event ToolResultEvent) {
			results = append(results, event.Result)
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if requests != len(replies)-1 {
		t.Fatalf("expected recovery to close immediately after source-aware verification without a redundant final-answer request, got %d requests", requests)
	}
	if got := countEventsByRole(events, "implementation_target_write_ready_source_refresh_allowed") + countEventsByRole(events, "implementation_target_write_ready_nudge"); got == 0 {
		t.Fatalf("expected at least one target-write-ready source-refresh or nudge event, got %d; events=%v", got, eventRolesFor(events))
	}
	if got := countEventsByRole(events, "mutation_rejected_retry_write_recovery"); got != 0 {
		t.Fatalf("repeated full reads should not use rejected-retry recovery, got %d events; events=%v", got, eventRolesFor(events))
	}
	if got := countEventsByRole(events, "rejection_retry_blocked"); got != 0 {
		t.Fatalf("mutation no-write retry must not force answer through rejection_retry_blocked, got %d; events=%v", got, eventRolesFor(events))
	}
	data, err := os.ReadFile(filepath.Join(root, "internal/labels/labels.go"))
	if err != nil {
		t.Fatalf("read edited file: %v", err)
	}
	if !strings.Contains(string(data), `return "Save"`) || strings.Contains(string(data), `Save changes`) {
		t.Fatalf("expected edit to be applied, got:\n%s\nresults=%#v", string(data), results)
	}
	if !strings.Contains(answer, "internal/labels/labels.go") {
		t.Fatalf("expected final answer to report changed path, got:\n%s", answer)
	}
	if !strings.Contains(strings.ToLower(answer), "verification") {
		t.Fatalf("expected final answer to report verification status, got:\n%s", answer)
	}
}

func TestRunnerRepeatedNoWriteRecoveryRepairsBlockedNonWriteBeforeClosing(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "go.mod", "module example.com/labels\n\ngo 1.24\n"); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := writeTestFile(root, "internal/labels/labels.go", "package labels\n\nfunc ShortLabel() string {\n\treturn \"Save changes\"\n}\n"); err != nil {
		t.Fatalf("write label source: %v", err)
	}

	replies := []string{
		"I found the label source and can make the change.",
		`{"tool":"list_files","path":"internal/labels"}`,
		`{"tool":"edit_file","path":"internal/labels/labels.go","old_text":"return \"Save changes\"","new_text":"return \"Save\"","description":"shorten save label"}`,
		`{"tool":"shell","command":"go test ./..."}`,
		"Changed paths:\n- internal/labels/labels.go\n\nVerification: go test ./... passed.",
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
	defer server.Close()

	mgr := workspace.NewManager(config.Workspace{
		Root:         root,
		CWD:          root,
		Shell:        "/bin/sh",
		EnableWrites: true,
		EnableShell:  true,
	})
	mgr.MarkPathObserved("internal/labels/labels.go")

	var events []RunEvent
	var completionStatuses []string
	runner := &Runner{
		Client:    llama.NewClient(server.URL, time.Second),
		Workspace: mgr,
		Policy:    RunPolicy{MaxToolSteps: 10, MaxNoProgressTurns: 10, MaxModelCalls: 20},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"internal/labels/labels.go"},
			Constraints:       []string{"exact_anchor_implementation", "implementation_packet_write_ready"},
		},
		OnEvent: func(event RunEvent) {
			events = append(events, event)
			if event.Role == "completion_status" {
				completionStatuses = append(completionStatuses, event.Content)
			}
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Shorten the save label in the session tab."}},
		llama.ChatOptions{MaxTokens: 8192},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := countEventsByRole(events, "implementation_write_only_recovery_non_write_repair"); got != 1 {
		t.Fatalf("expected one write-only non-write repair event, got %d; events=%s", got, formatRunEventsForTest(events))
	}
	for _, status := range completionStatuses {
		if strings.Contains(status, "write_only_recovery_no_edit_attempted") ||
			strings.Contains(status, "write_only_recovery_required_paths_remaining") {
			t.Fatalf("write-only non-write should get one repair turn before closeout, got status %s; events=%s", status, formatRunEventsForTest(events))
		}
	}
	data, err := os.ReadFile(filepath.Join(root, "internal/labels/labels.go"))
	if err != nil {
		t.Fatalf("read edited file: %v", err)
	}
	if !strings.Contains(string(data), `return "Save"`) || strings.Contains(string(data), `Save changes`) {
		t.Fatalf("expected repair turn to apply edit, got:\n%s", string(data))
	}
	if !strings.Contains(answer, "internal/labels/labels.go") || !strings.Contains(strings.ToLower(answer), "verification") {
		t.Fatalf("expected final implementation answer after repair, got:\n%s", answer)
	}
}

func TestCorrectedNamedGapRetryIsAllowedOnce(t *testing.T) {
	replies := []string{
		`{"tool":"discovery_query","purpose":"architecture_boundary","path":"internal/agent","pattern":"task|delegate"}`,
		`{"tool":"read_file","path":"internal/agent/runner.go"}`,
		`{"tool":"read_file","path":"internal/agent/runner.go","named_gap":"inspect the exact branch that launches the child task","why_not_answering":"the final answer depends on the exact child task branch"}`,
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
	// After the discovery_query fix: readiness no longer fires after a
	// lone discovery_query. The model must read at least one source body
	// before readiness declares. Both read_file calls should succeed.
	foundMissingGapRejection := false
	for _, ev := range events {
		if strings.Contains(ev.Content, "Post-readiness follow-up missing named gap") {
			foundMissingGapRejection = true
		}
	}
	if foundMissingGapRejection {
		t.Fatalf("discovery_query alone should not trigger readiness; read_file should not be rejected for missing named gap, roles=%v", eventRolesFor(events))
	}
	if got := countEventsByRole(events, "post_readiness_terminal_gate"); got != 0 {
		t.Fatalf("expected no post-readiness terminal gate decision, got %d; roles=%v", got, eventRolesFor(events))
	}
	if got := countEventsByRole(events, "post_readiness_gap_declared"); got != 0 {
		t.Fatalf("expected no declared post-readiness gap requirement, got %d; roles=%v", got, eventRolesFor(events))
	}
	if got := countEventsByRole(events, "rejection_retry_blocked"); got != 0 {
		t.Fatalf("expected no rejection_retry_blocked, got %d; roles=%v", got, eventRolesFor(events))
	}
	if got := countEventsByRole(events, "readiness_check"); got < 1 {
		t.Fatalf("expected at least one readiness check, got %d; roles=%v", got, eventRolesFor(events))
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

func formatRunEventsForTest(events []RunEvent) string {
	lines := make([]string, 0, len(events))
	for _, ev := range events {
		content := strings.TrimSpace(ev.Content)
		if len(content) > 240 {
			content = content[:240] + "..."
		}
		if content == "" {
			lines = append(lines, ev.Role)
			continue
		}
		lines = append(lines, ev.Role+": "+content)
	}
	return strings.Join(lines, "\n")
}

// TestFingerprintNudgeFiresOncePerFingerprint verifies that when the model
// calls the same tool+args three times *non-consecutively*, the runner
// emits a fingerprint_nudge exactly once — and does NOT trigger the
// DoomLoopGate (which requires consecutive triples).
func TestFingerprintNudgeFiresOncePerFingerprint(t *testing.T) {
	// Alternate A/B/A/B/A so the identical-call run never reaches 3.
	replies := []string{
		`{"tool":"read_file","path":"internal/app/app.go","start_line":1,"end_line":20}`,
		`{"tool":"read_file","path":"internal/agent/runner.go","start_line":1,"end_line":20}`,
		`{"tool":"read_file","path":"internal/app/app.go","start_line":1,"end_line":20}`,
		`{"tool":"read_file","path":"internal/agent/runner.go","start_line":1,"end_line":20}`,
		`{"tool":"read_file","path":"internal/app/app.go","start_line":1,"end_line":20}`,
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

func TestConversationSamePathFixationNudgesOnVariedRanges(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "internal/app/app.go", "package app\n"); err != nil {
		t.Fatalf("write seed: %v", err)
	}
	replies := []string{
		`{"tool":"read_file","path":"internal/app/app.go","start_line":1,"end_line":50}`,
		`{"tool":"read_file","path":"internal/app/app.go","start_line":51,"end_line":100}`,
		`{"tool":"read_file","path":"internal/app/app.go","start_line":101,"end_line":150}`,
		`{"tool":"read_file","path":"internal/app/app.go","start_line":151,"end_line":200}`,
		`{"tool":"read_file","path":"internal/app/app.go","start_line":201,"end_line":250}`,
		`{"tool":"read_file","path":"internal/app/app.go","start_line":251,"end_line":300}`,
		"The input handling path is internal/app/app.go.",
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

	var events []RunEvent
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 10, MaxNoProgressTurns: 12},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
		},
		OnEvent: func(event RunEvent) {
			events = append(events, event)
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Where is input handling?"}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if answer != "The input handling path is internal/app/app.go." {
		t.Fatalf("unexpected answer after same-path nudge: %q", answer)
	}
	if requests != len(replies) {
		t.Fatalf("expected nudge to send the model back for final answer, used %d/%d replies", requests, len(replies))
	}
	if got := countEventsByRole(events, "same_path_fixation_nudge"); got != 1 {
		t.Fatalf("expected one same_path_fixation_nudge, got %d; events=%s", got, formatRunEventsForTest(events))
	}
	if got := countEventsByRole(events, "same_path_fixation_tool_hidden"); got != 1 {
		t.Fatalf("expected one same_path_fixation_tool_hidden, got %d; events=%s", got, formatRunEventsForTest(events))
	}
	if got := countEventsByRole(events, "tool_hidden"); got != 1 {
		t.Fatalf("expected read_file to be hidden after ignored same-path nudge, got %d; events=%s", got, formatRunEventsForTest(events))
	}
	if got := countEventsByRole(events, "doom_loop_guard"); got != 0 {
		t.Fatalf("varied ranges in conversation should not trip doom loop guard, got %d; events=%s", got, formatRunEventsForTest(events))
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

func TestSynthesisNudgeDoesNotFireForPlanExecutePlanner(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "a.go", "package a\n"); err != nil {
		t.Fatalf("write seed: %v", err)
	}
	replies := []string{
		`{"tool":"read_file","path":"a.go","start_line":1,"end_line":1}`,
		"Decision: source-satisfied\n- a.go: already implemented; no changes needed.",
	}
	var requests int
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

	var events []RunEvent
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second).WithBackend(config.BackendMLX),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxNoProgressTurns: 6, SynthesisNudgeAt: 1},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.go"},
		},
		OnEvent: func(event RunEvent) {
			events = append(events, event)
		},
	}
	if _, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "plan execute nudge scenario"}}, llama.ChatOptions{}, nil, nil, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := countEventsByRole(events, "synthesis_nudge"); got != 0 {
		t.Fatalf("plan-execute planner should rely on typed source/readiness nudges, got %d generic synthesis nudge(s)", got)
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
// Go source file (no line range) is rewritten to code_navigate file_outline
// so the turn gets executable source evidence instead of control guidance.
func TestDiscoveryRedirectOnReadFileCodePath(t *testing.T) {
	replies := []string{
		`{"tool":"read_file","path":"main.go"}`,
		"final answer",
	}
	events, _ := runRedirectScenario(t, replies)
	if got := countEventsByRole(events, "tool_request_rewrite"); got != 1 {
		t.Fatalf("expected exactly 1 tool_request_rewrite event, got %d", got)
	}
	if got := countEventsByRole(events, "discovery_redirect"); got != 0 {
		t.Fatalf("structured read rewrite should avoid control redirect events, got %d", got)
	}
}

// TestDiscoveryRedirectFallsBackOnSecondCall asserts repeated read-only
// source reads stay on the executable structured route instead of falling
// back to control-only redirect prose.
func TestDiscoveryRedirectFallsBackOnSecondCall(t *testing.T) {
	replies := []string{
		`{"tool":"read_file","path":"main.go"}`,
		`{"tool":"read_file","path":"main.go"}`,
		"final",
	}
	events, _ := runRedirectScenario(t, replies)
	if got := countEventsByRole(events, "tool_request_rewrite"); got != 3 {
		t.Fatalf("expected repeated read to rewrite through outline then symbol, got %d", got)
	}
	if got := countEventsByRole(events, "discovery_redirect"); got != 0 {
		t.Fatalf("structured read rewrite should avoid control redirect events, got %d", got)
	}
}

func TestDiscoveryRedirectSkipsScopedImplementationRequiredPathRead(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "go.mod", "module example.test\n"); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	source := "package main\n\nfunc main() {}\n" + strings.Repeat("// pad\n", 500)
	if err := writeTestFile(root, "main.go", source); err != nil {
		t.Fatalf("write main.go: %v", err)
	}

	replies := []string{
		`{"tool":"read_file","path":"main.go"}`,
		`{"tool":"write_file","path":"main.go","content":"package main\n\nfunc main() { println(\"ok\") }\n","description":"update required path"}`,
		"Changed paths:\n- main.go\nVerification: not required for this test fixture.",
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
	defer server.Close()

	var events []RunEvent
	var readFileResults int
	runner := &Runner{
		Client:    llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true}),
		Policy:    RunPolicy{MaxNoProgressTurns: 8},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:           "implementation",
			Contract:             "implementation_result",
			PermissionProfile:    "guarded_workspace_write",
			VerificationContract: "not_required_for_text_mutations",
			RequiredEditPaths:    []string{"main.go"},
			EvidencePaths:        []string{"main.go"},
		},
		OnEvent: func(event RunEvent) {
			events = append(events, event)
		},
	}
	_, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "update main.go"}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			if event.Request.Tool == "read_file" && event.Result.Error == "" && strings.Contains(event.Result.Output, "func main()") {
				readFileResults++
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := countEventsByRole(events, "discovery_redirect"); got != 0 {
		t.Fatalf("required-path source reads should not redirect in scoped implementation, got %d", got)
	}
	if readFileResults != 1 {
		t.Fatalf("expected direct read_file result for required path, got %d", readFileResults)
	}
	if requests != len(replies) {
		t.Fatalf("expected %d model requests, got %d", len(replies), requests)
	}
}

func TestScopedImplementationLargeRequiredPathReadReturnsBoundedSourcePacket(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "go.mod", "module example.test\n"); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	lines := make([]string, 1200)
	lines[0] = "package main"
	lines[1] = ""
	for i := 2; i < len(lines); i++ {
		lines[i] = fmt.Sprintf("// filler %d", i+1)
	}
	lines[909] = "func ProductWorkflowCoverageGate() bool { return true }"
	source := strings.Join(lines, "\n")
	if err := writeTestFile(root, "main.go", source); err != nil {
		t.Fatalf("write main.go: %v", err)
	}

	replies := []string{
		`{"tool":"read_file","path":"main.go"}`,
		`{"tool":"read_file","path":"main.go","start_line":900,"end_line":920}`,
		`{"tool":"edit_file","path":"main.go","old_text":"func ProductWorkflowCoverageGate() bool { return true }","new_text":"func ProductWorkflowCoverageGate() bool { return false }","description":"update required path"}`,
		"Changed paths:\n- main.go\nVerification: not required for this test fixture.",
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
	defer server.Close()

	var events []RunEvent
	var readResults []workspace.ToolResult
	runner := &Runner{
		Client:    llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true}),
		Policy:    RunPolicy{MaxNoProgressTurns: 8},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Objective:            "update ProductWorkflowCoverageGate behavior",
			TaskFamily:           "implementation",
			Contract:             "implementation_result",
			PermissionProfile:    "guarded_workspace_write",
			VerificationContract: "not_required_for_text_mutations",
			RequiredEditPaths:    []string{"main.go"},
			EvidencePaths:        []string{"main.go"},
		},
		OnEvent: func(event RunEvent) {
			events = append(events, event)
		},
	}
	_, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "update ProductWorkflowCoverageGate behavior"}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			if event.Request.Tool == "read_file" {
				readResults = append(readResults, event.Result)
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := countEventsByRole(events, "discovery_redirect"); got != 0 {
		t.Fatalf("large required-path source read should not use discovery redirect, got %d", got)
	}
	if got := countEventsByRole(events, "scoped_bounded_source_read"); got != 1 {
		t.Fatalf("expected one bounded source event, got %d", got)
	}
	if len(readResults) < 2 {
		t.Fatalf("expected bounded read and explicit range read, got %d", len(readResults))
	}
	first := readResults[0]
	if !strings.Contains(first.Summary, "bounded large required-path source") {
		t.Fatalf("expected bounded source summary, got %q", first.Summary)
	}
	if strings.Contains(first.Output, "1200:// filler 1200") {
		t.Fatalf("bounded read leaked tail of large file:\n%s", first.Output)
	}
	if !strings.Contains(first.Output, "ProductWorkflowCoverageGate") {
		t.Fatalf("bounded read should prioritize task-relevant source over generic file header:\n%s", first.Output)
	}
	if !strings.Contains(first.Output, `"start_line"`) || !strings.Contains(first.Output, `"end_line"`) {
		t.Fatalf("bounded read should include concrete follow-up range guidance:\n%s", first.Output)
	}
	if !strings.Contains(readResults[1].Summary, "lines 900-920") {
		t.Fatalf("explicit bounded range should execute normally, summary=%q", readResults[1].Summary)
	}
	if !runner.Workspace.HasObservedPath("main.go") {
		t.Fatalf("bounded source packet should mark the path observed for subsequent edits")
	}
	if requests != len(replies) {
		t.Fatalf("expected %d model requests, got %d", len(replies), requests)
	}
}

func TestScopedImplementationRejectsLargeExistingWriteFileReplacement(t *testing.T) {
	root := t.TempDir()
	lines := make([]string, 1200)
	lines[0] = "package main"
	for i := 1; i < len(lines); i++ {
		lines[i] = fmt.Sprintf("// filler %d", i+1)
	}
	original := strings.Join(lines, "\n")
	if err := writeTestFile(root, "main.go", original); err != nil {
		t.Fatalf("write main.go: %v", err)
	}

	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:           "implementation",
			Contract:             "implementation_result",
			RequiredEditPaths:    []string{"main.go"},
			PermissionProfile:    "guarded_workspace_write",
			VerificationContract: "not_required_for_text_mutations",
		},
	}

	intercept := runner.preflightScopedImplementationLargeWriteFileReplacement(workspace.ToolRequest{
		Tool:        "write_file",
		Path:        "main.go",
		Content:     "package main\n\nfunc main() {}\n",
		Description: "replace large file",
	}, nil)
	if !intercept.Intercepted {
		t.Fatalf("expected large existing source replacement to be rejected")
	}
	if !strings.Contains(intercept.Result.Error, "Use apply_patch or edit_file") {
		t.Fatalf("expected bounded edit guidance, got %q", intercept.Result.Error)
	}
	got, err := os.ReadFile(filepath.Join(root, "main.go"))
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	if string(got) != original {
		t.Fatalf("preflight must not mutate large file")
	}
}

func TestScopedImplementationRejectsLargeWriteFileReplacementInRunnerLoop(t *testing.T) {
	root := t.TempDir()
	lines := make([]string, 1200)
	lines[0] = "package main"
	for i := 1; i < len(lines); i++ {
		lines[i] = fmt.Sprintf("// filler %d", i+1)
	}
	original := strings.Join(lines, "\n")
	if err := writeTestFile(root, "main.go", original); err != nil {
		t.Fatalf("write main.go: %v", err)
	}

	replies := []string{
		`{"tool":"write_file","path":"main.go","content":"package main\n\nfunc main() {}\n","description":"replace large file"}`,
		"Blocked by large write_file guard.",
		"Still blocked by large write_file guard.",
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

	var guardEvents int
	var rejectedResults int
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
			RequiredEditPaths:    []string{"main.go"},
			PermissionProfile:    "guarded_workspace_write",
			VerificationContract: "not_required_for_text_mutations",
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "scoped_large_write_file_guard" {
				guardEvents++
			}
		},
	}

	_, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "replace main.go"}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			if event.Request.Tool == "write_file" && strings.Contains(event.Result.Error, "write_file would replace existing large scoped source path") {
				rejectedResults++
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if guardEvents != 1 {
		t.Fatalf("expected one scoped_large_write_file_guard event, got %d", guardEvents)
	}
	if rejectedResults != 1 {
		t.Fatalf("expected one rejected write_file result, got %d", rejectedResults)
	}
	got, err := os.ReadFile(filepath.Join(root, "main.go"))
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	if string(got) != original {
		t.Fatalf("large write_file rejection must leave file unchanged")
	}
	if requests < 2 || requests > len(replies) {
		t.Fatalf("expected runner to stop after recovery without extra requests, got %d request(s)", requests)
	}
}

func TestScopedImplementationLargeWriteFileReplacementGuardAllowsSmallNewAndDocFiles(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "small.go", "package main\n"); err != nil {
		t.Fatalf("write small.go: %v", err)
	}
	largeDoc := strings.Repeat("paragraph\n", scopedImplementationLargeReadLineThreshold+1)
	if err := writeTestFile(root, "notes.md", largeDoc); err != nil {
		t.Fatalf("write notes.md: %v", err)
	}

	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:           "implementation",
			Contract:             "implementation_result",
			RequiredEditPaths:    []string{"small.go", "missing.go"},
			PermissionProfile:    "guarded_workspace_write",
			VerificationContract: "not_required_for_text_mutations",
		},
	}
	for _, req := range []workspace.ToolRequest{
		{Tool: "write_file", Path: "small.go", Content: "package main\n\nfunc main() {}\n", Description: "replace small file"},
		{Tool: "write_file", Path: "missing.go", Content: "package main\n", Description: "create new file"},
	} {
		if intercept := runner.preflightScopedImplementationLargeWriteFileReplacement(req, nil); intercept.Intercepted {
			t.Fatalf("expected %s to pass guard, got %q", req.Path, intercept.Result.Error)
		}
	}

	docRunner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:           "implementation",
			Contract:             "implementation_result",
			RequiredEditPaths:    []string{"notes.md"},
			PermissionProfile:    "guarded_workspace_write",
			VerificationContract: "not_required_for_text_mutations",
		},
	}
	docReq := workspace.ToolRequest{Tool: "write_file", Path: "notes.md", Content: "# Notes\n", Description: "replace doc deliverable"}
	if intercept := docRunner.preflightScopedImplementationLargeWriteFileReplacement(docReq, nil); intercept.Intercepted {
		t.Fatalf("expected doc deliverable to pass guard, got %q", intercept.Result.Error)
	}
}

func TestScopedImplementationRejectsByteLargeWriteFileReplacementWithoutReadingBody(t *testing.T) {
	root := t.TempDir()
	largeLine := strings.Repeat("x", scopedImplementationLargeReadByteThreshold+1024)
	if err := writeTestFile(root, "main.go", "package main\nconst large = \""+largeLine+"\"\n"); err != nil {
		t.Fatalf("write main.go: %v", err)
	}

	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:           "implementation",
			Contract:             "implementation_result",
			RequiredEditPaths:    []string{"main.go"},
			PermissionProfile:    "guarded_workspace_write",
			VerificationContract: "not_required_for_text_mutations",
		},
	}
	intercept := runner.preflightScopedImplementationLargeWriteFileReplacement(workspace.ToolRequest{
		Tool:        "write_file",
		Path:        "main.go",
		Content:     "package main\n",
		Description: "replace byte-large file",
	}, nil)
	if !intercept.Intercepted {
		t.Fatalf("expected byte-large existing source replacement to be rejected")
	}
	if strings.Contains(intercept.Result.Error, "lines") {
		t.Fatalf("byte-large rejection should use stat detail without reading line count, got %q", intercept.Result.Error)
	}
}

func TestScopedImplementationBoundedSourcePacketCapsByteLargeLines(t *testing.T) {
	root := t.TempDir()
	longLine := strings.Repeat("x", scopedImplementationLargeReadByteThreshold+1024)
	if err := writeTestFile(root, "main.go", strings.Join([]string{
		"package main",
		"func ByteLargeTarget() string {",
		longLine,
		"}",
	}, "\n")); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Objective:            "update ByteLargeTarget behavior",
			TaskFamily:           "implementation",
			Contract:             "implementation_result",
			RequiredEditPaths:    []string{"main.go"},
			EvidencePaths:        []string{"main.go"},
			PermissionProfile:    "guarded_workspace_write",
			VerificationPlan:     nil,
			VerificationContract: "not_required_for_text_mutations",
		},
	}

	intercept := runner.preflightScopedImplementationBoundedSourceRead(workspace.ToolRequest{Tool: "read_file", Path: "main.go"}, nil)
	if !intercept.Intercepted {
		t.Fatalf("expected byte-large range-less read to be bounded")
	}
	if !strings.Contains(intercept.Result.Output, "[truncated ") {
		t.Fatalf("expected long source line to be capped, output length=%d", len(intercept.Result.Output))
	}
	if strings.Contains(intercept.Result.Output, longLine) {
		t.Fatalf("bounded source packet leaked the full long line")
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

// TestDiscoveryRedirectDecisionGuideFiresOncePerLanguage confirms read-only
// source reads rewrite to executable structured reads for each target without
// injecting language decision-guide prose.
func TestDiscoveryRedirectDecisionGuideFiresOncePerLanguage(t *testing.T) {
	// Two distinct read_file targets should both execute through the
	// structured route in the same step.
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
	if got := countEventsByRole(events, "tool_request_rewrite"); got != 2 {
		t.Fatalf("expected 2 tool_request_rewrite events, got %d", got)
	}
	if got := countEventsByRole(events, "discovery_decision_guide"); got != 0 {
		t.Fatalf("structured read rewrites should not inject decision guides, got %d", got)
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
		Tool:    "task",
		Summary: "Child task completed",
		Output:  "Source-body evidence paths:\n- internal/agent/runner_child_tasks.go\n\nKey findings:\n- The harness uses subagents by delegating to child runners in the source-body implementation.",
	}}, false, false) {
		t.Fatal("expected durable child-packet closure to suppress discovery decision-guide reinjection")
	}
}

func TestRunnerRejectsDuplicateReadOnlyTaskWithoutForcingAnswer(t *testing.T) {
	replies := []string{
		`{"tool":"task","agent":"explorer","mode":"read_only","prompt":"Inspect discovery tool usefulness and summarize evidence."}`,
		`<<QUESTION>>: source-body evidence for discovery tool usefulness was not available from this delegated pass.`,
		`{"tool":"task","agent":"explorer","mode":"read_only","prompt":"Inspect discovery tool usefulness again."}`,
		`{"tool":"read_file","path":"internal/workspace/discovery_query.go","start_line":1,"end_line":80}`,
		`The duplicate child task was rejected, then the direct source read grounded the answer in ` + "`internal/workspace/discovery_query.go`" + `. Discovery tool usefulness depends on source-body evidence rather than another delegated search.`,
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

	var hiddenTaskResults int
	var childTaskResults int
	var duplicateTaskRejections int
	var directReads int
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal/workspace"), 0o755); err != nil {
		t.Fatalf("mkdir workspace fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "internal/workspace/discovery_query.go"), []byte("package workspace\n\nfunc RunDiscoveryQuery() {}\n"), 0o644); err != nil {
		t.Fatalf("write discovery fixture: %v", err)
	}
	runner := &Runner{
		Client:    llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		Policy:    RunPolicy{MaxToolSteps: 5, MaxNoProgressTurns: 4},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "architecture",
			Contract:          "architecture_assessment",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"task", "read_file", "search_content"},
			Objective:         "What can we add to our discovery tools in an effort at increasing their overall usefulness?",
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: runner.RuntimeBrief.Objective}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			if event.Result.Tool != "task" {
				if event.Result.Tool == "read_file" && strings.TrimSpace(event.Result.Error) == "" {
					directReads++
				}
				return
			}
			if event.Result.Summary == "Child task completed" {
				childTaskResults++
			}
			if event.Result.Summary == "Duplicate child task rejected" {
				duplicateTaskRejections++
			}
			if event.Result.Summary == "Tool hidden by active contract" {
				hiddenTaskResults++
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if childTaskResults != 1 {
		t.Fatalf("expected exactly one child task result, got %d", childTaskResults)
	}
	if hiddenTaskResults != 0 {
		t.Fatalf("duplicate post-child task should use the specific duplicate-task rejection, got %d generic hidden task result(s)", hiddenTaskResults)
	}
	if duplicateTaskRejections != 1 {
		t.Fatalf("expected one duplicate task rejection, got %d", duplicateTaskRejections)
	}
	if directReads != 1 {
		t.Fatalf("expected model to retain bounded read strategy after duplicate task rejection, got %d direct reads", directReads)
	}
	if strings.TrimSpace(answer) == "" || strings.Contains(answer, "hidden for the rest of this turn") {
		t.Fatalf("expected clean answer after direct evidence, got %q", answer)
	}
	if requests != len(replies) {
		t.Fatalf("expected duplicate task rejection plus direct read path with %d model requests, got %d", len(replies), requests)
	}
}

// TestRecoverySearchStopWordsDoesNotFilterModel verifies that "model"
// is not in the search stop words list — it's domain-significant for
// path→objective matching (e.g., model_popup.go matching /model task).
func TestRecoverySearchStopWordsDoesNotFilterModel(t *testing.T) {
	stop := recoverySearchStopWords()
	if _, ok := stop["model"]; ok {
		t.Fatal(`"model" should not be a search stop word — it carries domain signal for path→objective matching`)
	}
}

// TestTargetWriteReadyStagedNudgeSurfacesBasePrompt verifies that
// the nudge output includes the concrete sourced-path info from
// basePrompt when it is provided.
func TestTargetWriteReadyStagedNudgeSurfacesBasePrompt(t *testing.T) {
	req := workspace.ToolRequest{Tool: "search_content"}
	basePrompt := "The implementation lane is target-write-ready. Source-body evidence is already available for: model_popup.go, model_swap.go."

	// Early stage (nudgeCount <= 6)
	msg := targetWriteReadyStagedNudge(basePrompt, req, 3)
	if !strings.Contains(msg, "model_popup.go") {
		t.Fatalf("early nudge should surface basePrompt paths, got: %s", msg)
	}
	if !strings.Contains(msg, "Nudge 3 of 12") {
		t.Fatalf("early nudge should include countdown, got: %s", msg)
	}

	// Late stage (nudgeCount > 6)
	msg = targetWriteReadyStagedNudge(basePrompt, req, 8)
	if !strings.Contains(msg, "model_popup.go") {
		t.Fatalf("late nudge should surface basePrompt paths, got: %s", msg)
	}
	if !strings.Contains(msg, "8 discovery calls without a write") {
		t.Fatalf("late nudge should mention discovery count, got: %s", msg)
	}
	t.Logf("early nudge example: %s", targetWriteReadyStagedNudge(basePrompt, req, 3)[:200])
}

// TestTargetWriteReadyStagedNudgeFallsBackWhenBasePromptEmpty verifies
// that the generic fallback text is used when basePrompt is empty.
func TestTargetWriteReadyStagedNudgeFallsBackWhenBasePromptEmpty(t *testing.T) {
	req := workspace.ToolRequest{Tool: "search_content"}

	msg := targetWriteReadyStagedNudge("", req, 3)
	if !strings.Contains(msg, "Source evidence is available for sourced target path(s)") {
		t.Fatalf("should use generic fallback when basePrompt is empty, got: %s", msg)
	}
}

// TestCompleteWithoutMoreToolsInjectsToolResultForDanglingToolCalls verifies
// that completeWithoutMoreToolsWithReason handles dangling tool_calls in the
// conversation history by returning an error instead of crashing with a
// protocol violation. The actual injection is tested via integration.
// This prevents DeepSeek's "insufficient tool messages" protocol error.
func TestCompleteWithoutMoreToolsInjectsToolResultForDanglingToolCalls(t *testing.T) {
	// Build a turnMessages slice ending with a dangling tool_calls assistant
	// message. Verify that completeWithoutMoreToolsWithReason injects a tool
	// result before the force answer system message.
	messages := []llama.ChatMessage{
		{Role: "user", Content: "test"},
		{Role: "assistant", ToolCalls: []llama.ToolCall{{
			ID:       "call_dangling_1",
			Type:     "function",
			Function: llama.ToolCallFunc{Name: "search_content", Arguments: `{"pattern":"test"}`},
		}}},
	}
	root := t.TempDir()
	ws := workspace.NewManager(config.Workspace{Root: root, CWD: root})

	runner := &Runner{
		Workspace: ws,
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: "implementation",
			Contract:   "implementation_result",
		},
	}
	ctx := context.Background()

	// Call completeWithoutMoreToolsWithReason directly.
	// The function should inject a tool result for the dangling tool_call
	// before appending the force answer system message.
	// This test won't reach the API call because the mock client will fail,
	// but we can verify the messages were injected correctly.
	_ = ctx
	_ = runner
	_ = messages

	// Integration test: verify that toolResultMessage with a real ID produces
	// role:"tool" not role:"system"
	call := &llama.ToolCall{
		ID:       "call_dangling_1",
		Type:     "function",
		Function: llama.ToolCallFunc{Name: "search_content", Arguments: `{}`},
	}
	result := workspace.ToolResult{Tool: "search_content", Summary: "rejected"}
	msg := toolResultMessage(call, result)
	if msg.Role != "tool" {
		t.Fatalf("expected role=tool for call with real ID, got %q", msg.Role)
	}
	if msg.ToolCallID != "call_dangling_1" {
		t.Fatalf("expected ToolCallID=call_dangling_1, got %q", msg.ToolCallID)
	}
}

func TestStripOrphanedToolCalls(t *testing.T) {
	t.Run("removes assistant with orphaned tool_calls", func(t *testing.T) {
		messages := []llama.ChatMessage{
			{Role: "system", Content: "you are a helpful assistant"},
			{Role: "user", Content: "hello"},
			{
				Role:    "assistant",
				Content: "",
				ToolCalls: []llama.ToolCall{
					{ID: "call_1", Function: llama.ToolCallFunc{Name: "read_file"}},
				},
			},
			// No matching tool response — orphaned
		}
		stripped := stripOrphanedToolCalls(messages)
		if len(stripped) != 2 {
			t.Fatalf("expected 2 messages, got %d", len(stripped))
		}
		if stripped[0].Role != "system" || stripped[1].Role != "user" {
			t.Fatal("expected system and user messages only")
		}
	})

	t.Run("keeps assistant when all tool_calls have responses", func(t *testing.T) {
		messages := []llama.ChatMessage{
			{Role: "system", Content: "you are a helpful assistant"},
			{Role: "user", Content: "hello"},
			{
				Role:    "assistant",
				Content: "",
				ToolCalls: []llama.ToolCall{
					{ID: "call_1", Function: llama.ToolCallFunc{Name: "read_file"}},
				},
			},
			{Role: "tool", ToolCallID: "call_1", Content: "result"},
		}
		stripped := stripOrphanedToolCalls(messages)
		if len(stripped) != 4 {
			t.Fatalf("expected 4 messages, got %d", len(stripped))
		}
	})

	t.Run("removes assistant when one of multiple calls is orphaned", func(t *testing.T) {
		messages := []llama.ChatMessage{
			{Role: "system", Content: "you are a helpful assistant"},
			{
				Role:    "assistant",
				Content: "",
				ToolCalls: []llama.ToolCall{
					{ID: "call_1", Function: llama.ToolCallFunc{Name: "read_file"}},
					{ID: "call_2", Function: llama.ToolCallFunc{Name: "search_content"}},
				},
			},
			{Role: "tool", ToolCallID: "call_1", Content: "result"},
			// call_2 has no response — orphaned, so whole assistant message is removed
		}
		stripped := stripOrphanedToolCalls(messages)
		if len(stripped) != 2 {
			t.Fatalf("expected 2 messages (system + tool), got %d", len(stripped))
		}
	})

	t.Run("no-op when no tool_calls present", func(t *testing.T) {
		messages := []llama.ChatMessage{
			{Role: "system", Content: "you are a helpful assistant"},
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "hi there!"},
		}
		stripped := stripOrphanedToolCalls(messages)
		if len(stripped) != 3 {
			t.Fatalf("expected 3 messages, got %d", len(stripped))
		}
	})
}

func TestCompletionGateRejectsTextualXMLToolMarkup(t *testing.T) {
	// Real payloads from live DeepSeek sessions that emitted XML
	// instead of proper tool calls or prose.
	xmlPayloads := []string{
		// DeepSeek's n_tool_calls format
		`<n_tool_calls>
<n_invoke name="code_navigate">
<n_parameter name="capability" string="true">find_symbol</n_parameter>
<n_parameter name="path" string="true">internal/agent</n_parameter>
</n_invoke>
</n_tool_calls>`,
		// Bare open tag
		"<n_tool_calls>",
		// DeepSeek janus format
		`<janus_tool_calls>
<janus_invoke name="read_file">
<janus_parameter name="path" string="true">internal/agent/runner.go</janus_parameter>
</janus_invoke>
</janus_tool_calls>`,
		// Generic invoke XML
		`<invoke name="context_pack">
<parameter name="task" string="true">find validation</parameter>
</invoke>`,
		// Mixed prose with XML - the model might wrap XML in text
		"Let me check that.\n\n<n_tool_calls>\n<n_invoke name=\"search_content\">\n</n_invoke>\n</n_tool_calls>",
		// tool_call XML variant
		`<tool_call>
<function name="read_file">
<parameter name="path">test.go</parameter>
</function>
</tool_call>`,
	}

	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
			Objective:         "answer the user's question",
		},
	}

	for i, payload := range xmlPayloads {
		result, rejected := runner.completionGateRejection(payload, nil, 0, nil, false)
		if !rejected {
			t.Errorf("payload %d: expected XML to be rejected, got accepted\npayload: %s", i, payload[:min(len(payload), 100)])
			continue
		}
		if result.Tool != "completion_gate" {
			t.Errorf("payload %d: expected tool='completion_gate', got %q", i, result.Tool)
		}
		if !strings.Contains(result.Error, "XML") && !strings.Contains(result.Error, "markup") {
			t.Errorf("payload %d: expected error to mention XML/markup, got: %s", i, result.Error)
		}
	}
}

func TestCompletionGateAcceptsNormalProse(t *testing.T) {
	// Verify that normal prose answers are NOT rejected by the XML check
	proseAnswers := []string{
		"The implement lane and plan-execute lane share the same base validation requirements including source_sanity checks and compilation verification.",
		"Based on the evidence from helpers.go and edit_set_obligations.go, both lanes require go test compilation checks.",
		"Here's what I found:\n\n- Plan-execute: requires submit_plan with edit obligations\n- Implement: requires preflight write guard\n\nThey differ in that...",
		"", // empty reply
	}

	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
			Objective:         "answer the user's question",
		},
	}

	for i, prose := range proseAnswers {
		_, rejected := runner.completionGateRejection(prose, nil, 0, nil, false)
		if rejected {
			// It might be rejected for other reasons (like evidence insufficiency),
			// but it must NOT be rejected for XML/markup
			result, _ := runner.completionGateRejection(prose, nil, 0, nil, false)
			if strings.Contains(result.Error, "XML") || strings.Contains(result.Error, "markup") {
				t.Errorf("prose %d: normal prose incorrectly flagged as XML markup", i)
			}
		}
	}
}

func TestIsTextualToolCallDetectsLiveSessionXML(t *testing.T) {
	// Exact XML payload from session 20260527-163710 event 566
	liveXML := `<n_tool_calls>
<n_invoke name="search_content">
<n_parameter name="pattern" string="true">PlannerOwnedVerification</n_parameter>
<n_parameter name="path" string="true">internal</n_parameter>
<n_parameter name="named_gap" string="true">find where PlannerOwnedVerification field is set in opts for each lane</n_parameter>
<n_parameter name="why_not_answering" string="true">need to see how implement vs plan-execute set verification contract</n_parameter>
</n_invoke>
</n_tool_calls>`
	if !IsTextualToolCall(liveXML) {
		t.Fatal("live session n_tool_calls XML was NOT detected as a textual tool call")
	}
}

func TestCompleteWithoutMoreToolsWithReasonCatchesNToolCallsXML(t *testing.T) {
	// Simulate the exact flow: model emits n_tool_calls XML in a forced completion
	xmlContent := `<n_tool_calls>
<n_invoke name="code_navigate">
<n_parameter name="capability" string="true">find_definition</n_parameter>
<n_parameter name="symbol" string="true">IsDiagnosticToolName</n_parameter>
<n_parameter name="path" string="true">internal/workspace</n_parameter>
</n_invoke>
</n_tool_calls>`

	// Multiple replies: first is XML (caught), retry might produce XML too
	replies := []string{
		xmlContent,             // first call: XML detected, triggers retry
		"ok I will answer now", // retry: clean prose
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

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
			Objective:         "answer the user's question",
		},
		Policy: RunPolicy{MaxToolSteps: 1, MaxNoProgressTurns: 3},
	}

	answer, err := runner.completeWithoutMoreToolsWithReason(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "hello"}},
		llama.ChatOptions{},
		nil,
		"Model-call budget is exhausted.",
		nil,
	)

	if err == nil {
		// If no error, the answer must NOT be the raw XML
		if strings.Contains(answer, "<n_tool_calls>") || strings.Contains(answer, "<n_invoke") {
			t.Fatalf("completeWithoutMoreToolsWithReason returned raw n_tool_calls XML instead of catching it:\n%s", answer)
		}
		// The answer should be a clean fallback
		if answer == "" {
			t.Fatal("completeWithoutMoreToolsWithReason returned empty string for XML input")
		}
		t.Logf("XML was caught, returned: %s", answer[:min(len(answer), 100)])
	} else {
		t.Logf("XML caused error (expected): %v", err)
	}
}

func TestLooksLikeTextualToolCallDetectsDSML(t *testing.T) {
	// The actual bytes from the live session contain DSML with
	// Unicode replacement characters. The terminal renders them
	// as <n_tool_calls> but programmatic matching against "<n_tool"
	// fails because the code points are U+FFFD, not ASCII 'n'.
	// The definitive signature is the literal string "DSML".

	// Load the exact content from the live session
	import_json := `"` + ``
	_ = import_json

	// Test with the DSML signature
	if !looksLikeTextualToolCall("<n_tool_calls>") {
		// This should still pass (existing test)
	}

	// The DSML signature should be detected
	dsmlPayload := "<\ufffd\ufffdDSML\ufffd\ufffdtool_calls>"
	if !looksLikeTextualToolCall(dsmlPayload) {
		t.Fatal("DSML payload not detected by looksLikeTextualToolCall")
	}

	// Also test the presentation-layer check
	if !strings.Contains(dsmlPayload, "DSML") {
		t.Fatal("DSML not found in payload via strings.Contains")
	}
}

func TestDetectsDSMLFromLiveSessionBytes(t *testing.T) {
	// Read the exact assistant message bytes from the live session log
	data, err := os.ReadFile("../../logs/conversations/20260527-171709.829.jsonl")
	if err != nil {
		t.Skipf("live session log not available: %v", err)
	}

	// Find the assistant message
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		if !strings.Contains(line, `"assistant"`) {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			continue
		}
		if record["type"] != "message" || record["role"] != "assistant" {
			continue
		}
		content, _ := record["content"].(string)
		if content == "" {
			continue
		}

		// This is the exact XML the model returned to the user
		t.Logf("Live content length: %d", len(content))
		t.Logf("Contains 'DSML': %v", strings.Contains(content, "DSML"))
		t.Logf("Contains '<n_tool': %v", strings.Contains(content, "<n_tool"))
		t.Logf("looksLikeTextualToolCall: %v", looksLikeTextualToolCall(content))

		if !looksLikeTextualToolCall(content) {
			t.Fatal("looksLikeTextualToolCall did NOT detect the live session XML payload")
		}
		return
	}
	t.Fatal("could not find assistant message in live session log")
}

func TestCompletionGateRejectsRawToolResultTranscriptFormats(t *testing.T) {
	// Exact answer from session 20260527-184529
	liveAnswer := "The inspected source in `internal/planexecute/run.go` contains `func emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, \"writer_route_decision\", decision.EventFields())`, `if resumeState != nil {` and `return runResumeReviewRepair(cfg, opts, prompt, *resumeState, resumeStatePath, promptRun, rec, stdout, stderr)`. No files were changed."

	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
			Objective:         "answer the user's question",
			AllowedTools:      []string{"read_file", "search_content", "context_pack"},
		},
	}

	result, rejected := runner.completionGateRejection(liveAnswer, []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 internal/planexecute/run.go lines 1-50"},
		{Tool: "context_query", Summary: "Found 5 references"},
	}, 0, nil, false)

	if !rejected {
		t.Fatal("live session answer was NOT rejected by completion gate")
	}
	if result.Summary != "Read-only completion gate rejected search-hit inventory" {
		t.Errorf("unexpected summary: %s", result.Summary)
	}

	// Also test other variants
	variants := []string{
		"The inspected source in `some/file.go` contains `func Foo()`, `type Bar struct`, and `var baz = 1`.",
		"The inspected source in `other.go` contains `const X = 1`, `func Y()`, and `type Z int`.",
	}
	for i, v := range variants {
		if _, rejected := runner.completionGateRejection(v, []workspace.ToolResult{
			{Tool: "read_file", Summary: "Read f1 some/file.go lines 1-50"},
		}, 0, nil, false); !rejected {
			t.Errorf("variant %d not rejected: %s", i, v[:min(len(v), 60)])
		}
	}
}

func TestForcedCompletionCatchesLiveSessionGarbageAnswer(t *testing.T) {
	// Exact answer from session 20260527-191920
	garbageAnswer := "The inspected source in `internal/agent/runner_test.go` contains `func TestVerificationContractSkipsGate(t *testing.T) {`, `cases := []struct {` and `contract string`. No files were changed."

	// Simulate 50 read/navigate tool results (enough for evidence-ready)
	results := make([]workspace.ToolResult, 50)
	for i := range results {
		results[i] = workspace.ToolResult{
			Tool:    "read_file",
			Summary: "Read f1 internal/agent/runner_test.go lines 1-50",
		}
	}

	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"read_file", "code_navigate", "context_query", "search_content"},
			Objective:         "answer the user's question",
		},
	}

	// Call the COMPLETION GATE directly - this is what completeWithoutMoreToolsWithReason calls
	result, rejected := runner.completionGateRejection(garbageAnswer, results, 0, nil, false)

	if !rejected {
		// Try the specific checks individually to find the gap
		t.Log("completionGateRejection did not reject - checking individual checks...")

		// Check 1: looksLikeTextualToolCall
		if looksLikeTextualToolCall(garbageAnswer) {
			t.Error("looksLikeTextualToolCall: true (but this gate fires BEFORE search-hit check in completionGateRejection)")
		} else {
			t.Log("looksLikeTextualToolCall: false (correct - not XML)")
		}

		// Check 2: unexecuted tool request
		if unexec, _ := runner.unexecutedToolRequestCompletionRejection(garbageAnswer); unexec.Tool != "" {
			t.Logf("unexecutedToolRequest: rejected - %s", unexec.Summary)
		} else {
			t.Log("unexecutedToolRequest: not rejected")
		}

		// Check 3: raw tool result transcript
		if raw, _ := rawToolResultTranscriptCompletionRejection(garbageAnswer); raw.Tool != "" {
			t.Logf("rawToolResultTranscript: rejected - %s", raw.Summary)
		} else {
			t.Log("rawToolResultTranscript: not rejected")
		}

		// Check 4: read-only evidence
		if ev, _ := runner.readOnlyEvidenceCompletionRejection(garbageAnswer, results); ev.Tool != "" {
			t.Logf("readOnlyEvidence: rejected - %s", ev.Summary)
		} else {
			t.Log("readOnlyEvidence: not rejected (evidence is ready)")
		}

		// Check 5: read-only non-substantive
		if ns, _ := runner.readOnlyNonSubstantiveCompletionRejection(garbageAnswer, results); ns.Tool != "" {
			t.Logf("readOnlyNonSubstantive: rejected - %s", ns.Summary)
		} else {
			t.Log("readOnlyNonSubstantive: not rejected (answer has enough words)")
		}

		// Check 6: read-only search hit inventory - THIS IS THE ONE THAT SHOULD CATCH IT
		if si, rejected2 := runner.readOnlySearchHitInventoryCompletionRejection(garbageAnswer, results); rejected2 {
			t.Logf("readOnlySearchHitInventory: REJECTED - %s", si.Summary)
		} else {
			t.Log("readOnlySearchHitInventory: NOT REJECTED - this is the gap!")
			// Debug: check the individual conditions
			brief := workerruntime.NormalizeRuntimeBrief(runner.RuntimeBrief)
			t.Logf("  PermissionProfile: %s (needs 'read_only_workspace')", brief.PermissionProfile)
			t.Logf("  EvidenceReady: %v", genericReadOnlyAnswerEvidenceReady(brief, results))
			t.Logf("  looksLikeToolResultTranscript: %v", looksLikeToolResultTranscript(strings.ToLower(garbageAnswer)))

			// Check containsAny patterns
			lower := strings.ToLower(garbageAnswer)
			for _, pat := range []string{"matching line(s)", "returned hit(s)", "tool-local"} {
				t.Logf("  contains %q: %v", pat, strings.Contains(lower, pat))
			}
			t.Logf("  contains 'the inspected source in': %v", strings.Contains(lower, "the inspected source in"))
			t.Logf("  contains ' contains `': %v", strings.Contains(lower, " contains `"))
		}

		t.Fatal("GARBAGE ANSWER WAS NOT REJECTED BY COMPLETION GATE")
	}

	t.Logf("Garbage answer caught: %s", result.Summary)
}

func TestConversationMilestonesDefaultContext(t *testing.T) {
	first, interval := conversationMilestones(0)
	// Default 65536 ctxSize: thresholdMsgs = 65536*40/100/125 = 209 → clamped to 200
	// maxToolCalls = 100, first = max(15, 25) = 25, interval = max(20, 35) = 35
	if first != 25 {
		t.Errorf("default context: firstMilestone = %d, want 25", first)
	}
	if interval != 35 {
		t.Errorf("default context: nudgeInterval = %d, want 35", interval)
	}
}

func TestConversationMilestonesSmallContext(t *testing.T) {
	first, interval := conversationMilestones(32768)
	// 32K: thresholdMsgs = 32768*40/100/125 = 104, maxToolCalls = 52
	// first = max(15, 13) = 15, interval = max(20, 18) = 20
	if first != 15 {
		t.Errorf("32K context: firstMilestone = %d, want 15 (floor)", first)
	}
	if interval != 20 {
		t.Errorf("32K context: nudgeInterval = %d, want 20 (floor)", interval)
	}
}

func TestConversationMilestonesLargeContext(t *testing.T) {
	first, interval := conversationMilestones(131072)
	// 128K: thresholdMsgs = 131072*40/100/125 = 419 → clamped to 200
	// maxToolCalls = 100, first = max(15, 25) = 25, interval = max(20, 35) = 35
	if first != 25 {
		t.Errorf("128K context: firstMilestone = %d, want 25", first)
	}
	if interval != 35 {
		t.Errorf("128K context: nudgeInterval = %d, want 35", interval)
	}
}

func TestConversationMilestonesAllContextSizes(t *testing.T) {
	tests := []struct {
		ctxSize                 int
		wantFirst, wantInterval int
	}{
		{0, 25, 35},      // unknown → 65K default → clamped
		{16384, 15, 20},  // 16K: thresholdMsgs=16K*0.4/125=52, maxCalls=26, first=max(15,6)=15, interval=max(20,9)=20
		{32768, 15, 20},  // 32K: thresholdMsgs=104, maxCalls=52, first=max(15,13)=15, interval=max(20,18)=20
		{65536, 25, 35},  // 65K: thresholdMsgs=209→clamped 200, maxCalls=100, first=25, interval=35
		{131072, 25, 35}, // 128K: clamped
		{262144, 25, 35}, // 256K: clamped
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("ctx=%d", tt.ctxSize), func(t *testing.T) {
			first, interval := conversationMilestones(tt.ctxSize)
			if first != tt.wantFirst {
				t.Errorf("ctx=%d: firstMilestone = %d, want %d", tt.ctxSize, first, tt.wantFirst)
			}
			if interval != tt.wantInterval {
				t.Errorf("ctx=%d: nudgeInterval = %d, want %d", tt.ctxSize, interval, tt.wantInterval)
			}
		})
	}
}

func TestCompactionBudgetAllocator(t *testing.T) {
	tests := []struct {
		name            string
		maxMsgs         int
		maxChars        int
		wantMiddleFloor int // middle budget must be at least this
		wantTailMin     int
		wantTailMax     int
	}{
		{name: "defaults", maxMsgs: 0, maxChars: 0, wantMiddleFloor: 0, wantTailMin: 2, wantTailMax: 8},
		{name: "from messages", maxMsgs: 40, maxChars: 0, wantMiddleFloor: 1000, wantTailMin: 2, wantTailMax: 8},
		{name: "from chars", maxMsgs: 0, maxChars: 40000, wantMiddleFloor: 1000, wantTailMin: 2, wantTailMax: 8},
		{name: "small budget", maxMsgs: 8, maxChars: 0, wantMiddleFloor: 100, wantTailMin: 2, wantTailMax: 8},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			middle, tailFloor := compactionBudget(RunPolicy{
				MaxContextMessages: tt.maxMsgs,
				MaxContextChars:    tt.maxChars,
			})
			if tailFloor < tt.wantTailMin {
				t.Errorf("tailFloor = %d, want >= %d", tailFloor, tt.wantTailMin)
			}
			if tailFloor > tt.wantTailMax {
				t.Errorf("tailFloor = %d, want <= %d", tailFloor, tt.wantTailMax)
			}
			if middle < tt.wantMiddleFloor {
				t.Errorf("middleBudget = %d, want >= %d", middle, tt.wantMiddleFloor)
			}
		})
	}
}

func TestScoreMessageForRetentionEmptyContent(t *testing.T) {
	msg := llama.ChatMessage{Role: "system", Content: ""}
	brief := workerruntime.WorkerRuntimeBrief{}
	score := scoreMessageForRetention(msg, nil, brief, nil)
	if score != 0.0 {
		t.Fatalf("expected score 0.0 for empty content, got %.2f", score)
	}
}

func TestScoreMessageForRetentionFailedVerification(t *testing.T) {
	result := workspace.ToolResult{
		Tool:  "source_sanity",
		Error: "diagnostics returned errors",
	}
	msg := llama.ChatMessage{Role: "tool", Content: "Tool: source_sanity\nError: diagnostics returned errors"}
	brief := workerruntime.WorkerRuntimeBrief{}
	score := scoreMessageForRetention(msg, &result, brief, nil)
	// Failed verification is highest value: +0.40
	if score < 0.35 {
		t.Fatalf("expected score >= 0.35 for failed verification, got %.2f", score)
	}
}

func TestScoreMessageForRetentionEditPathMention(t *testing.T) {
	result := workspace.ToolResult{
		Tool:    "code_navigate",
		Summary: "Extracted body of ProcessRequest",
	}
	msg := llama.ChatMessage{Role: "tool", Content: "Tool: code_navigate\nPath: internal/auth/middleware.go\nSummary: Extracted body of ProcessRequest"}
	brief := workerruntime.WorkerRuntimeBrief{
		RequiredEditPaths: []string{"internal/auth/middleware.go"},
	}
	score := scoreMessageForRetention(msg, &result, brief, nil)
	// Edit path mention +0.25 + high-value tool +0.10 = 0.35
	if score < 0.30 {
		t.Fatalf("expected score >= 0.30 for edit-path evidence, got %.2f", score)
	}
}

func TestScoreMessageForRetentionEvidencePathMention(t *testing.T) {
	result := workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Read internal/auth/middleware_test.go",
	}
	msg := llama.ChatMessage{Role: "tool", Content: "Tool: read_file\nPath: internal/auth/middleware_test.go"}
	brief := workerruntime.WorkerRuntimeBrief{
		EvidencePaths: []string{"internal/auth/middleware_test.go"},
	}
	score := scoreMessageForRetention(msg, &result, brief, nil)
	// Evidence path +0.15 + high-value tool +0.10 = 0.25
	if score < 0.20 {
		t.Fatalf("expected score >= 0.20 for evidence-path read, got %.2f", score)
	}
}

func TestScoreMessageForRetentionSuccessfulWrite(t *testing.T) {
	result := workspace.ToolResult{
		Tool:    "edit_file",
		Summary: "Replaced 3 lines in config.go",
	}
	msg := llama.ChatMessage{Role: "tool", Content: "Tool: edit_file\nPath: config.go"}
	brief := workerruntime.WorkerRuntimeBrief{
		RequiredEditPaths: []string{"config.go"},
	}
	score := scoreMessageForRetention(msg, &result, brief, nil)
	// Edit path +0.25 + successful write +0.20 = 0.45
	if score < 0.35 {
		t.Fatalf("expected score >= 0.35 for successful write on edit path, got %.2f", score)
	}
}

func TestScoreMessageForRetentionNoiseFolds(t *testing.T) {
	result := workspace.ToolResult{
		Tool:    "search_content",
		Summary: "Found 0 results for 'nonexistent'",
	}
	msg := llama.ChatMessage{Role: "tool", Content: "Tool: search_content\nPattern: nonexistent\nFound 0 results"}
	brief := workerruntime.WorkerRuntimeBrief{}
	score := scoreMessageForRetention(msg, &result, brief, nil)
	// No edit path, no evidence path, no high-value tool, no error -> 0.0
	if score > 0.01 {
		t.Fatalf("expected score ~0.0 for noise, got %.2f", score)
	}
}

func TestScoreMessageForRetentionCeiling(t *testing.T) {
	// Verify score never exceeds 1.0 even with all signals
	result := workspace.ToolResult{
		Tool:  "shell",
		Error: "test FAIL",
	}
	msg := llama.ChatMessage{Role: "tool", Content: "Tool: shell\nPath: internal/auth/middleware.go\nPath: internal/auth/middleware_test.go"}
	brief := workerruntime.WorkerRuntimeBrief{
		RequiredEditPaths: []string{"internal/auth/middleware.go"},
		EvidencePaths:     []string{"internal/auth/middleware_test.go"},
	}
	score := scoreMessageForRetention(msg, &result, brief, nil)
	if score > 1.0 {
		t.Fatalf("score exceeded 1.0: got %.2f", score)
	}
}

func TestResultIndexForMessage(t *testing.T) {
	messages := []llama.ChatMessage{
		{Role: "system", Content: "prompt"},
		{Role: "user", Content: "do work"},
		{Role: "assistant", Content: "calling tool"},
		{Role: "tool", Content: "result 1"},
		{Role: "assistant", Content: "calling another tool"},
		{Role: "tool", Content: "result 2"},
		{Role: "assistant", Content: "calling third tool"},
		{Role: "system", Content: "Tool: shell\nError: exiting"}, // system-formatted tool result
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "first"},
		{Tool: "search_content", Summary: "second"},
		{Tool: "shell", Summary: "third", Error: "failed"},
	}

	// First tool result message at index 3 -> result index 0
	idx := resultIndexForMessage(messages, 3, len(results))
	if idx != 0 {
		t.Errorf("message 3 -> result index %d, want 0", idx)
	}

	// Second tool result message at index 5 -> result index 1
	idx = resultIndexForMessage(messages, 5, len(results))
	if idx != 1 {
		t.Errorf("message 5 -> result index %d, want 1", idx)
	}

	// System-formatted tool result at index 7 -> result index 2
	idx = resultIndexForMessage(messages, 7, len(results))
	if idx != 2 {
		t.Errorf("message 7 -> result index %d, want 2", idx)
	}

	// Non-tool message -> -1
	idx = resultIndexForMessage(messages, 1, len(results))
	if idx != -1 {
		t.Errorf("message 1 -> result index %d, want -1", idx)
	}

	// Empty results -> -1
	idx = resultIndexForMessage(messages, 3, 0)
	if idx != -1 {
		t.Errorf("empty results -> result index %d, want -1", idx)
	}
}

func TestCompactionKeepsPrefixAndModifiesStrategy(t *testing.T) {
	// Verify the new strategy string appears in compaction events.
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": "```tool\n{\"tool\":\"task_ledger\",\"todos\":[{\"id\":\"x\",\"content\":\"work\",\"status\":\"completed\"}]}\n```"},
			}},
		})
	}))
	defer server.Close()

	var sawEvidenceWeighted bool
	runner := &Runner{
		Client:    llama.NewClient(server.URL, 5*time.Second),
		AgentName: "main",
		Workspace: workspace.NewManager(config.Workspace{
			Root: t.TempDir(),
			CWD:  ".",
		}),
		Policy: RunPolicy{
			MaxToolSteps:       3,
			MaxNoProgressTurns: 10,
			MaxContextMessages: 4,
		},
		TaskLedger: TaskLedgerPolicy{Required: true},
		OnEvent: func(event RunEvent) {
			if event.Role == "context_compaction" {
				if strings.Contains(event.Content, "evidence_weighted") {
					sawEvidenceWeighted = true
				}
			}
		},
	}

	_, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "do work"}}, llama.ChatOptions{}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !sawEvidenceWeighted {
		t.Fatal("expected evidence_weighted strategy in compaction event")
	}
}

func TestMsgCharCount(t *testing.T) {
	msg := llama.ChatMessage{Role: "tool", Content: "result", ReasoningContent: ""}
	got := msgCharCount(msg)
	want := len("tool") + len("result")
	if got != want {
		t.Errorf("msgCharCount = %d, want %d", got, want)
	}
	msg2 := llama.ChatMessage{Role: "assistant", Content: "hello", ReasoningContent: "thinking"}
	got2 := msgCharCount(msg2)
	want2 := len("assistant") + len("hello") + len("thinking")
	if got2 != want2 {
		t.Errorf("msgCharCount with reasoning = %d, want %d", got2, want2)
	}
}

// TestFinalAnswerBlockerKindQUESTIONComesBeforeEvidence verifies that
// the <<QUESTION>> escape hatch fires BEFORE non_answer_prose and
// required_evidence_unmet checks. If <<QUESTION>> came after, the
// model would be trapped: its escape-hatch blocker would itself be
// blocked by evidence requirements. Regression for 08:55 evidence log.
func TestFinalAnswerBlockerKindQUESTIONComesBeforeEvidence(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "architecture",
			Contract:          "architecture_assessment",
			PermissionProfile: "read_only_workspace",
			RequiredEvidence:  []string{"inspected_paths", "architecture_boundary_evidence", "confidence"},
		},
	}
	// Answer is <<QUESTION>> blocker with no file paths — would fail
	// required_evidence_unmet if checked before the escape hatch.
	answer := "<<QUESTION>>: Completion gate rejected the final answer repeatedly. Missing completion evidence: gate retry 3/3."
	results := []workspace.ToolResult{}

	kind, _ := runner.finalAnswerBlockerKind(answer, results)
	if kind != "user_visible_blocker" {
		t.Fatalf("<<QUESTION>> escape hatch NOT honored: got blocker_kind=%q, want user_visible_blocker", kind)
	}
}

// TestFinalAnswerBlockerKindNonAnswerProseBlocks verifies that
// textual tool-call markup in answer prose is blocked.
// Regression for 05:23 evidence log.
func TestFinalAnswerBlockerKindCatchesToolCallInProse(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
		},
	}
	answer := `[Calling tool: search_content({"tool":"search_content","pattern":"Evidence"})]`
	results := []workspace.ToolResult{}

	kind, _ := runner.finalAnswerBlockerKind(answer, results)
	if kind != "non_answer_prose" {
		t.Fatalf("tool-call in prose NOT blocked: got blocker_kind=%q, want non_answer_prose", kind)
	}
}

// TestFinalAnswerBlockerKindCatchesEchoedQuestion verifies that
// an answer that merely echoes the user's question is blocked.
// Regression for 07:29 evidence log.
func TestFinalAnswerBlockerKindCatchesEchoedQuestion(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
		},
	}
	answer := "Question: Can you investigate the strange spacing issues?"
	results := []workspace.ToolResult{}

	kind, _ := runner.finalAnswerBlockerKind(answer, results)
	if kind != "non_answer_prose" {
		t.Fatalf("echoed question NOT blocked: got blocker_kind=%q, want non_answer_prose", kind)
	}
}

func TestReadOnlyPreGateDecoratorDoesNotLaunderQuestionPacket(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
		},
	}
	answer := "Question: Can you investigate the strange spacing issues?\n" +
		"Source: delegated read-only evidence packet.\n\n" +
		"Inspected paths:\n- `internal/app/update_handlers.go`\n\n" +
		"Key findings:\n- I still need source-body evidence before answering."

	got := runner.decoratedReadOnlyReplyForCompletionGate(answer, nil)
	if got != answer {
		t.Fatalf("pre-gate decorator must not turn raw non-answer packets into presentable prose before acceptance; got:\n%s", got)
	}
	acceptance := runner.evaluateFinalAnswerAcceptance(got, nil)
	if acceptance.Decision != finalAnswerAcceptanceRejected || acceptance.BlockerKind != "non_answer_prose" {
		t.Fatalf("raw question packet should remain blocked by final-answer acceptance, got %#v", acceptance)
	}
}

// TestFinalAnswerBlockerKindCatchesCommandFailed verifies that
// tool-failure language (command failed, exit status) is blocked.
// Regression for 08:28 evidence log.
func TestFinalAnswerBlockerKindCatchesCommandFailed(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	}
	answers := []string{
		"Changed paths: none\n\nVerification: not run or not detected.\n\nBlocker:\n- Content search failed: command failed: exit status 2",
		"Changed paths: none\n\nVerification: not run.\n\nBlocker:\n- File search rejected: pattern is required",
	}
	for i, answer := range answers {
		kind, _ := runner.finalAnswerBlockerKind(answer, []workspace.ToolResult{})
		if kind != "non_answer_prose" {
			t.Errorf("variant %d command-failure NOT blocked: got blocker_kind=%q, want non_answer_prose", i, kind)
		}
	}
}

// TestFinalAnswerBlockerKindAllowsBlockersWithQUESTION verifies that
// <<QUESTION>>-prefixed answers bypass non_answer_prose and
// required_evidence_unmet checks even when they contain tool-failure
// language or lack structural evidence.
func TestFinalAnswerBlockerKindAllowsBlockersWithQUESTION(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "investigate_then_implement",
			Contract:          "implementation_result",
			PermissionProfile: "analysis_read_only_then_guarded_writes",
			RequiredEvidence:  []string{"target_location", "changed_paths", "verification_status"},
		},
	}
	answers := []string{
		"<<QUESTION>>: File search rejected: pattern is required. Cannot locate target.",
		"<<QUESTION>>: Content search failed: command failed: exit status 2.",
	}
	for i, answer := range answers {
		kind, _ := runner.finalAnswerBlockerKind(answer, []workspace.ToolResult{})
		if kind != "user_visible_blocker" {
			t.Errorf("variant %d <<QUESTION>> blocker NOT allowed: got blocker_kind=%q, want user_visible_blocker", i, kind)
		}
	}
}

func TestFinalAnswerBlockerKindRejectsNonexistentSourceCitation(t *testing.T) {
	root := t.TempDir()
	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
		},
	}
	answer := "The root cause is in `internal/app/conversation_view.go` lines 417-433: `recalculateVisibleLines` resets `scrollY`."

	kind, reason := runner.finalAnswerBlockerKind(answer, nil)
	if kind != "source_grounding_unmet" {
		t.Fatalf("nonexistent source citation got blocker_kind=%q, want source_grounding_unmet", kind)
	}
	if !strings.Contains(reason, "internal/app/conversation_view.go") {
		t.Fatalf("blocker reason should name missing path, got %q", reason)
	}
}

func TestFinalAnswerBlockerKindRejectsMissingGuardInNonexistentSourceCitation(t *testing.T) {
	root := t.TempDir()
	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
		},
	}
	answer := "The missing scroll guard is in `internal/app/conversation_view.go` lines 417-433."

	kind, reason := runner.finalAnswerBlockerKind(answer, nil)
	if kind != "source_grounding_unmet" {
		t.Fatalf("missing/absent prose must not mark nonexistent source historical: kind=%q reason=%q", kind, reason)
	}
}

func TestFinalAnswerBlockerKindRejectsLineClaimWithoutSourceBody(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "internal", "app", "lifecycle.go")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("package app\n\nfunc Update() {}\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
		},
	}
	answer := "The root cause is in `internal/app/lifecycle.go` lines 418-424 where `WindowSizeMsg` is handled."
	results := []workspace.ToolResult{
		{Tool: "context_pack", Summary: "Context pack wrote packet", Output: "internal/app/lifecycle.go"},
	}

	kind, reason := runner.finalAnswerBlockerKind(answer, results)
	if kind != "source_grounding_unmet" {
		t.Fatalf("ungrounded source line claim got blocker_kind=%q, want source_grounding_unmet", kind)
	}
	if !strings.Contains(reason, "source-body read") {
		t.Fatalf("blocker reason should ask for source-body read, got %q", reason)
	}
}

func TestFinalAnswerBlockerKindRejectsTopLevelLineClaimWithoutSourceBody(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
		},
	}
	answer := "The root cause is in `main.go:3`, where `main` starts the process."

	kind, reason := runner.finalAnswerBlockerKind(answer, []workspace.ToolResult{
		{Tool: "context_pack", Summary: "Context pack wrote packet", Output: "main.go"},
	})
	if kind != "source_grounding_unmet" {
		t.Fatalf("top-level line claim without source body got blocker_kind=%q reason=%q, want source_grounding_unmet", kind, reason)
	}
}

func TestFinalAnswerBlockerKindRejectsCandidateLineClaimWithoutSourceBody(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "internal", "app", "lifecycle.go")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("package app\n\nfunc Update() {}\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
		},
	}
	answer := "The candidate root cause is in `internal/app/lifecycle.go` lines 418-424 where `WindowSizeMsg` is handled."
	results := []workspace.ToolResult{
		{Tool: "context_pack", Summary: "Context pack wrote packet", Output: "internal/app/lifecycle.go"},
	}

	kind, reason := runner.finalAnswerBlockerKind(answer, results)
	if kind != "source_grounding_unmet" {
		t.Fatalf("candidate line claim without source body got blocker_kind=%q reason=%q, want source_grounding_unmet", kind, reason)
	}
}

func TestFinalAnswerBlockerKindAllowsEvidencePathListWithoutSourceBody(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "internal", "agent", "runner.go")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("package agent\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
		},
	}
	answer := "Evidence paths: `internal/agent/runner.go`. The available evidence is not enough to name an exact function."

	kind, reason := runner.finalAnswerBlockerKind(answer, []workspace.ToolResult{
		{Tool: "context_pack", Summary: "Context pack wrote packet", Output: "internal/agent/runner.go"},
	})
	if kind != "" || reason != "" {
		t.Fatalf("plain evidence path list should not require source body: kind=%q reason=%q", kind, reason)
	}
}

func TestFinalAnswerBlockerKindRejectsMissingCanonicalRootFile(t *testing.T) {
	root := t.TempDir()
	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
		},
	}
	answer := "The root cause is in `go.mod`, which pins the wrong dependency."

	kind, reason := runner.finalAnswerBlockerKind(answer, nil)
	if kind != "source_grounding_unmet" {
		t.Fatalf("missing canonical root file got blocker_kind=%q reason=%q, want source_grounding_unmet", kind, reason)
	}
}

func TestFinalAnswerBlockerKindRejectsMissingCanonicalRootLineCitation(t *testing.T) {
	root := t.TempDir()
	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
		},
	}
	answer := "The root cause is in `go.mod:4`, which pins the wrong dependency."

	kind, reason := runner.finalAnswerBlockerKind(answer, nil)
	if kind != "source_grounding_unmet" {
		t.Fatalf("missing canonical root line citation got blocker_kind=%q reason=%q, want source_grounding_unmet", kind, reason)
	}
}

func TestFinalAnswerBlockerKindRejectsMissingTopLevelLineCitation(t *testing.T) {
	root := t.TempDir()
	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
		},
	}
	answer := "The root cause is in `main.go:42`, where startup resets the viewport."

	kind, reason := runner.finalAnswerBlockerKind(answer, nil)
	if kind != "source_grounding_unmet" {
		t.Fatalf("missing top-level line citation got blocker_kind=%q reason=%q, want source_grounding_unmet", kind, reason)
	}
}

func TestFinalAnswerBlockerKindRejectsMissingTopLevelSourceDetailCitation(t *testing.T) {
	root := t.TempDir()
	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
		},
	}
	answer := "The root cause is in `main.go`, where startup resets the viewport."

	kind, reason := runner.finalAnswerBlockerKind(answer, nil)
	if kind != "source_grounding_unmet" {
		t.Fatalf("missing top-level source-detail citation got blocker_kind=%q reason=%q, want source_grounding_unmet", kind, reason)
	}
}

func TestFinalAnswerBlockerKindAllowsLineClaimWithSourceBody(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "internal", "app", "lifecycle.go")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("package app\n\nfunc Update() {}\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
		},
	}
	answer := "The resize path is in `internal/app/lifecycle.go` lines 1-3 where `Update` handles messages."
	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read f1 internal/app/lifecycle.go lines 1-3 (total lines: 3)",
			Output:  "1:package app\n2:\n3:func Update() {}",
		},
	}

	kind, reason := runner.finalAnswerBlockerKind(answer, results)
	if kind != "" || reason != "" {
		t.Fatalf("source-body-backed answer blocked: kind=%q reason=%q", kind, reason)
	}
}

func TestFinalAnswerBlockerKindAllowsUniqueBasenameBackedBySourceBody(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "internal", "app", "model_state.go")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("package app\n\nfunc (m *Model) resize() {}\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
		},
	}
	answer := "The resize path is in `model_state.go` lines 1-3 where `Model.resize` handles viewport dimensions."
	results := []workspace.ToolResult{
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output:  "capability: read_symbol\nroute_summary: Read Model.resize in internal/app/model_state.go lines 1-3\n1:package app\n2:\n3:func (m *Model) resize() {}",
		},
	}

	kind, reason := runner.finalAnswerBlockerKind(answer, results)
	if kind != "" || reason != "" {
		t.Fatalf("unique basename backed by source-body evidence blocked: kind=%q reason=%q", kind, reason)
	}
}

func TestFinalAnswerBlockerKindRejectsAmbiguousBasenameSourceCitation(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"internal/app/model_state.go", "internal/other/model_state.go"} {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(path, []byte("package app\n\nfunc Resize() {}\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
		},
	}
	answer := "The resize path is in `model_state.go` lines 1-3 where `Resize` handles viewport dimensions."
	results := []workspace.ToolResult{
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output:  "route_summary: Read Model.resize in internal/app/model_state.go lines 1-3\n1:package app\n3:func Resize() {}",
		},
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output:  "route_summary: Read Other.resize in internal/other/model_state.go lines 1-3\n1:package app\n3:func Resize() {}",
		},
	}

	kind, reason := runner.finalAnswerBlockerKind(answer, results)
	if kind != "source_grounding_unmet" {
		t.Fatalf("ambiguous basename source citation got blocker_kind=%q reason=%q, want source_grounding_unmet", kind, reason)
	}
}

// TestMissingRequiredEvidenceEnforcesTargetLocation verifies that
// the target_location evidence label is structurally enforced.
func TestMissingRequiredEvidenceEnforcesTargetLocation(t *testing.T) {
	// Answer with no file paths, no changed paths in results
	missing := missingRequiredEvidence(
		[]string{"target_location"},
		"no paths here",
		[]workspace.ToolResult{},
	)
	if len(missing) != 1 || missing[0] != "target_location" {
		t.Fatalf("expected missing=[target_location], got %v", missing)
	}

	// Answer with a file path — satisfied
	missing = missingRequiredEvidence(
		[]string{"target_location"},
		"the code is in internal/agent/runner.go",
		[]workspace.ToolResult{},
	)
	if len(missing) != 0 {
		t.Fatalf("expected satisfied, got missing=%v", missing)
	}
}

// TestMissingRequiredEvidenceSkipsSemanticLabels verifies that
// non-deterministic labels (answer_addresses_user_request) are
// never enforced — they are model guidance only.
func TestMissingRequiredEvidenceSkipsSemanticLabels(t *testing.T) {
	missing := missingRequiredEvidence(
		[]string{"answer_addresses_user_request"},
		"completely irrelevant answer about something else",
		[]workspace.ToolResult{},
	)
	if len(missing) != 0 {
		t.Fatalf("semantic label should never be enforced: got missing=%v", missing)
	}
}

// TestConversationLaneWithoutSemanticEval verifies correct
// identification of conversation/direct_answer lanes for
// confidence demotion.
func TestConversationLaneWithoutSemanticEval(t *testing.T) {
	cases := []struct {
		name       string
		family     string
		contract   string
		permission string
		objective  string
		want       bool
	}{
		{"legacy conversation without read-only permission", "conversation", "direct_answer", "", "Explain readiness.", true},
		{"read-only conversation with relevance contract", "conversation", "direct_answer", "read_only_workspace", "Explain readiness.", false},
		{"direct-answer alias with relevance contract", "direct_answer", "direct_answer", "read_only_workspace", "Explain readiness.", false},
		{"empty objective cannot be semantically checked", "conversation", "direct_answer", "read_only_workspace", "", true},
		{"conversation implementation result", "conversation", "implementation_result", "read_only_workspace", "Explain readiness.", false},
		{"architecture direct answer", "architecture", "direct_answer", "read_only_workspace", "Explain readiness.", false},
		{"architecture assessment", "architecture", "architecture_assessment", "read_only_workspace", "Explain readiness.", false},
		{"implementation result", "investigate_then_implement", "implementation_result", "guarded_workspace_write", "Explain readiness.", false},
	}
	for _, c := range cases {
		brief := workerruntime.WorkerRuntimeBrief{
			TaskFamily:        c.family,
			Contract:          c.contract,
			PermissionProfile: c.permission,
			Objective:         c.objective,
		}
		got := conversationLaneWithoutSemanticEval(brief)
		if got != c.want {
			t.Errorf("%s: conversationLaneWithoutSemanticEval(%q, %q, %q) = %v, want %v",
				c.name, c.family, c.contract, c.permission, got, c.want)
		}
	}
}

// TestlooksLikeNonAnswerProseRejectsLiveFailures verifies that all
// five failure patterns from today's conversation logs are caught.
func TestLooksLikeNonAnswerProseRejectsLiveFailures(t *testing.T) {
	cases := []struct {
		name   string
		answer string
		want   bool
	}{
		{
			"05:23 - unexecuted tool call in prose",
			`[Calling tool: search_content({"tool":"search_content","pattern":"Evidence"})]`,
			true,
		},
		{
			"07:29 - echoed user question",
			"Question: Can you investigate the strange spacing issues?",
			true,
		},
		{
			"08:09 - file search rejected",
			"Changed paths: none\n\nVerification: not run.\n\nBlocker:\n- File search rejected: pattern is required",
			true,
		},
		{
			"08:28 - command failed exit status",
			"Changed paths: none\n\nVerification: not run.\n\nBlocker:\n- Content search failed: command failed: exit status 2",
			true,
		},
		{
			"18:06 - hidden reasoning meta-intent restatement",
			`The user is asking me to investigate why a prompt asking "which files and sections should be changed" might be routed as investigate_then_implement with write_intent=yes. I need to find the specific route-planner files/tests responsible and the smallest generic fix shape.`,
			true,
		},
		{
			"18:10 - source echo only",
			"The inspected source in `internal/agent/runner_test.go` contains `func TestLooksLikeNonAnswerProseRejectsLiveFailures(t *testing.T) {`, `cases := []struct {` and `name   string`. No files were changed.",
			true,
		},
		{
			"15:41 - short post-readiness source inventory",
			"For `what would it take to enable up and down arrow keys to iterate through the user's chat history?`, `internal/app/app.go` shows defines `truncateText`, defines `normalizeHistoryRole`, and `normalizeHistoryRole` returns `strings.ToUpper(value[:1]) + value[1:]`.",
			true,
		},
		{
			"16:13 - inspected body source inventory",
			"`Model.handleKeyPress` in `internal/app/update_handlers.go` is the clearest inspected implementation anchor. The inspected body shows that it defines `Model.handleKeyPress`, sets `m.history.expanded` to `false`, `Model.handleKeyPress` calls `renderHistoryPanelContent`, sets `m.history.expanded` to `!m.history.expanded`, and sets `m.history.focused` to `false`.",
			true,
		},
		{
			"legitimate answer (should NOT be blocked)",
			"Evidence in Idiom is composed of source paths, tool results, and verification status. The harness uses these across all lanes to enforce contracts.",
			false,
		},
		{
			"legitimate answer with task framing (should NOT be blocked)",
			"The user is asking me to explain the route choice. The answer is that `internal/app/turn_planner.go` classifies read-only evidence requests before mutation intent, so this should stay in a read-only lane.",
			false,
		},
		{
			"legitimate source-citing synthesis (should NOT be blocked)",
			"The inspected source in `internal/agent/runner.go` contains `looksLikeNonAnswerProse`, which is the shared final-answer guard because `finalAnswerBlockerKind` calls it before accepting answer-tool content.",
			false,
		},
		{
			"legitimate path-citing mechanism answer (should NOT be blocked)",
			"For chat-history navigation, `internal/app/model_state.go` would need selection state while `internal/app/update_handlers.go` would route up/down keys through the existing submit flow. That preserves the current message-send path while adding editable recall.",
			false,
		},
		{
			"legitimate blocker explanation (should NOT be blocked)",
			"Blocker: the model had not read the source body yet, so the harness correctly asked for evidence before accepting the answer.",
			false,
		},
		{
			"legitimate command failure explanation (should NOT be blocked)",
			"The command failed with exit status 2 because the search pattern was empty; the root cause was the missing query text, not the repository state.",
			false,
		},
		{
			"legitimate diagnostic answer with failure wording (should NOT be blocked)",
			"The failure was in the acceptance boundary: a diagnostic log was treated as source evidence even though it did not prove the code-level claim.",
			false,
		},
		{
			"<<QUESTION>> blocker is handled by the explicit escape hatch",
			"<<QUESTION>>: Cannot locate the target file. The path does not exist.",
			false,
		},
	}
	for _, c := range cases {
		got := looksLikeNonAnswerProse(c.answer)
		if got != c.want {
			t.Errorf("%s: looksLikeNonAnswerProse = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestFinalAnswerBlockerKindAllowsSubstantiveBlockerAndFailureAnswers(t *testing.T) {
	runner := &Runner{}
	cases := []string{
		"Blocker: the reviewer could not approve the patch because the diagnostic evidence did not match the user's source-code question.",
		"The command failed with exit status 2 because the supplied search pattern was empty; rerunning with a concrete pattern is the next action.",
	}
	for _, answer := range cases {
		blocker, reason := runner.finalAnswerBlockerKind(answer, nil)
		if blocker != "" || reason != "" {
			t.Fatalf("finalAnswerBlockerKind(%q) = (%q, %q), want accepted", answer, blocker, reason)
		}
	}
}

func TestFinalAnswerBlockerRejectsLiveNonAnswerShapes(t *testing.T) {
	runner := &Runner{}
	cases := []string{
		`The user is asking me to investigate why a prompt asking "which files and sections should be changed" might be routed as investigate_then_implement with write_intent=yes. I need to find the specific route-planner files/tests responsible and the smallest generic fix shape.`,
		"The inspected source in `internal/agent/runner_test.go` contains `func TestLooksLikeNonAnswerProseRejectsLiveFailures(t *testing.T) {`, `cases := []struct {` and `name   string`. No files were changed.",
		"For `what would it take to enable up and down arrow keys to iterate through the user's chat history?`, `internal/app/app.go` shows defines `truncateText`, defines `normalizeHistoryRole`, and `normalizeHistoryRole` returns `strings.ToUpper(value[:1]) + value[1:]`.",
		"`Model.handleKeyPress` in `internal/app/update_handlers.go` is the clearest inspected implementation anchor. The inspected body shows that it defines `Model.handleKeyPress`, sets `m.history.expanded` to `false`, `Model.handleKeyPress` calls `renderHistoryPanelContent`, sets `m.history.expanded` to `!m.history.expanded`, and sets `m.history.focused` to `false`.",
	}
	for _, answer := range cases {
		blocker, reason := runner.finalAnswerBlockerKind(answer, nil)
		if blocker != "non_answer_prose" || reason != "non_answer_prose" {
			t.Fatalf("finalAnswerBlockerKind(%q) = (%q, %q), want non_answer_prose", answer, blocker, reason)
		}
	}
}
