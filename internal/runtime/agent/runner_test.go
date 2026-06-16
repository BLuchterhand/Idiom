package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
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

	// ── Source-grounding (unsupported paths) ──────────────────────
	sgUnsupported := "the answer cites source path/line/symbol detail not backed by a source-body read: \"foo.go\". Use one exact read/code_navigate body probe for the claim, or qualify it as unverified."

	// Tier 1: should include "rewrite your answer" or "Rewrite your answer"
	got = escalateCompletionGateMessage(sgUnsupported, 1)
	for _, want := range []string{"[gate retry 2/3]", "not backed by a source-body read", "call one bounded"} {
		if !strings.Contains(got, want) {
			t.Errorf("source-grounding unsupported tier 1 expected to contain %q, got: %s", want, got)
		}
	}
	if strings.Contains(got, "use a different tool call") {
		t.Errorf("source-grounding unsupported tier 1 must not contain 'use a different tool call'; got: %s", got)
	}

	// Tier 2: should include read-file guidance, not "ONE concrete tool call"
	got = escalateCompletionGateMessage(sgUnsupported, 2)
	for _, want := range []string{"[gate retry 3/3", "`read_file` or `code_navigate`"} {
		if !strings.Contains(got, want) {
			t.Errorf("source-grounding unsupported tier 2 expected to contain %q, got: %s", want, got)
		}
	}
	if strings.Contains(got, "ONE concrete tool call") {
		t.Errorf("source-grounding unsupported tier 2 must not contain 'ONE concrete tool call'; got: %s", got)
	}

	// ── Source-grounding (non-existent paths) ─────────────────────
	sgMissing := "the answer cites path(s) that were discovered as index candidates but not read as source-body evidence: \"missing.go\". Correct the citation, label it as historical/log-only evidence, or answer from inspected current source."

	// Tier 1: should include read-file guidance
	got = escalateCompletionGateMessage(sgMissing, 1)
	for _, want := range []string{"[gate retry 2/3]", "not read as source-body evidence", "call one bounded"} {
		if !strings.Contains(got, want) {
			t.Errorf("source-grounding missing tier 1 expected to contain %q, got: %s", want, got)
		}
	}
	if strings.Contains(got, "use a different tool call") {
		t.Errorf("source-grounding missing tier 1 must not contain 'use a different tool call'; got: %s", got)
	}

	// Tier 2: should include read-file guidance, not "ONE concrete tool call"
	got = escalateCompletionGateMessage(sgMissing, 2)
	for _, want := range []string{"[gate retry 3/3", "`read_file` or `code_navigate`"} {
		if !strings.Contains(got, want) {
			t.Errorf("source-grounding missing tier 2 expected to contain %q, got: %s", want, got)
		}
	}
	if strings.Contains(got, "ONE concrete tool call") {
		t.Errorf("source-grounding missing tier 2 must not contain 'ONE concrete tool call'; got: %s", got)
	}

	// ── Non-source-grounding messages still get generic text ──────
	normalMsg := "the final answer did not satisfy the active blocker check; continue with concrete writes or a per-path blocker reason."
	got = escalateCompletionGateMessage(normalMsg, 1)
	for _, want := range []string{"[gate retry 2/3]", "use a different tool call"} {
		if !strings.Contains(got, want) {
			t.Errorf("non-source-grounding tier 1 expected to contain %q, got: %s", want, got)
		}
	}
	got = escalateCompletionGateMessage(normalMsg, 2)
	for _, want := range []string{"[gate retry 3/3", "ONE concrete tool call"} {
		if !strings.Contains(got, want) {
			t.Errorf("non-source-grounding tier 2 expected to contain %q, got: %s", want, got)
		}
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
		{req: workspace.ToolRequest{Tool: "shell", Command: "rg route internal/surfaces/tui/app"}, want: "shell_narrow:rg route internal/surfaces/tui/app"},
		{req: workspace.ToolRequest{Tool: "shell", Command: "git diff --check -- README.md"}, want: "shell_verification:git diff --check -- readme.md"},
		{req: workspace.ToolRequest{Tool: "read_file", ID: "f12"}, want: "read_file:f12"},
		{req: workspace.ToolRequest{Tool: "read_file", Path: "internal/surfaces/tui/app/app.go", StartLine: 10, EndLine: 40}, want: "read_file:internal/surfaces/tui/app/app.go:10-40"},
		{req: workspace.ToolRequest{Tool: "discovery_query", Purpose: "impact_analysis", Path: "internal/capabilities/workspace/manager.go", Pattern: "Execute", Language: "go"}, want: "discovery_query:impact_analysis:internal/capabilities/workspace/manager.go:execute:go"},
		{req: workspace.ToolRequest{Tool: "structural_search", Path: "internal", Pattern: "if $COND { $BODY }", Language: "go"}, want: "structural_search:internal:if $cond { $body }:go"},
		{req: workspace.ToolRequest{Tool: "dependency_graph", Path: ".", Language: "go"}, want: "dependency_graph:.:go"},
		{req: workspace.ToolRequest{Tool: "history_range", Path: "internal/capabilities/workspace/manager.go", StartLine: 10, EndLine: 40}, want: "history_range:internal/capabilities/workspace/manager.go:10-40"},
		{req: workspace.ToolRequest{Tool: "history_symbol", Path: "internal/capabilities/workspace/manager.go", Pattern: "Execute"}, want: "history_symbol:internal/capabilities/workspace/manager.go:execute"},
		{req: workspace.ToolRequest{Tool: "blame_range", Path: "internal/capabilities/workspace/manager.go", StartLine: 1, EndLine: 2}, want: "blame_range:internal/capabilities/workspace/manager.go:1-2"},
		{req: workspace.ToolRequest{Tool: "ctags_symbols", Path: ".", Pattern: "Manager"}, want: "ctags_symbols:.:manager"},
		{req: workspace.ToolRequest{Tool: "semgrep_scan", Path: ".", Pattern: "p/default"}, want: "semgrep_scan:.:p/default"},
		{req: workspace.ToolRequest{Tool: "joern_scan", Path: "src"}, want: "joern_scan:src"},
		{req: workspace.ToolRequest{Tool: "go_find_symbol", Pattern: "classifyPromptExecutionPlan"}, want: "go_find_symbol:classifypromptexecutionplan"},
		{req: workspace.ToolRequest{Tool: "go_file_outline", Path: "internal/surfaces/tui/app/app.go"}, want: "go_file_outline:internal/surfaces/tui/app/app.go"},
		{req: workspace.ToolRequest{Tool: "go_move_declarations", Path: "internal/surfaces/tui/app/app.go", Destination: "internal/surfaces/tui/app/rendering.go"}, want: "go_move_declarations:internal/surfaces/tui/app/app.go->internal/surfaces/tui/app/rendering.go"},
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
		Objective:         "What is in internal/runtime/agent/runner.go?",
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 internal/runtime/agent/runner.go lines 1-20",
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
		Objective:         "What is in internal/runtime/agent/runner.go?",
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
			Summary: `Blocked go_move_declarations with path:"internal/surfaces/tui/app/analysis_agents.go" and moves: {destination:"internal/surfaces/tui/app/orchestration.go", pattern:"tag:foo"}.`,
			Output:  `{"tool":"go_move_declarations","path":"internal/surfaces/tui/app/analysis_agents.go:12:3","moves":[{"destination":"internal/surfaces/tui/app/implementation.go"}]}`,
			Error:   "internal/surfaces/tui/app/analysis_agents.go:12:3: move was outside approval scope",
		},
	}, 8)

	if !slices.Contains(paths, "internal/surfaces/tui/app/analysis_agents.go") {
		t.Fatalf("expected source path to be pinned, got %#v", paths)
	}

	for _, forbidden := range []string{"internal/surfaces/tui/app/orchestration.go", "internal/surfaces/tui/app/implementation.go"} {
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
