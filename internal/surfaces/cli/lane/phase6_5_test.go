package lane

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"idiom/internal/platform/config"
)

// TestLaneRunsBriefLintByDefault verifies the Phase 6.5.1 contract:
// `idiom lane <mode>` runs the brief lint preflight automatically.
// Without this, every project would have to wrap the call in a
// shell pipeline that runs lint first — exactly the wrapper bloat
// Phase 6.5 was designed to retire.
//
// Bypasses runLaneMainTest because that helper installs its own
// no-op lint stub for the dispatch-shape tests; this test installs
// its own counting stub via runLaneMainWithDeps directly.
func TestLaneRunsBriefLintByDefault(t *testing.T) {
	briefPath := writeBrief(t, "# Test brief\n")
	var lintCalls int32
	stubLaneBriefLint(t, func(args []string, _, _ io.Writer) int {
		atomic.AddInt32(&lintCalls, 1)
		// Validate the lint runner saw the brief path.
		if len(args) != 1 || args[0] != briefPath {
			t.Errorf("lint runner expected [%q], got %v", briefPath, args)
		}
		return 0
	})

	var stdout, stderr bytes.Buffer
	fake := &laneFakeRunner{}
	cfg := config.Default()
	cfg.Workspace.EnableWrites = true
	deps := laneDeps{
		loadConfig:       func(string) (config.Config, error) { return cfg, nil },
		runPlanExecute:   fake.plan,
		runImplement:     fake.implement,
		defaultPromptRun: func(config.Config, string, runOptions, io.Writer) (string, error) { return "", nil },
	}
	code := runLaneMainWithDeps([]string{"plan-execute", briefPath}, &stdout, &stderr, deps)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d (stderr=%s)", code, stderr.String())
	}
	if got := atomic.LoadInt32(&lintCalls); got != 1 {
		t.Fatalf("expected lint to run exactly once, got %d calls", got)
	}
	if fake.calledMode != "plan-execute" {
		t.Fatalf("subcommand should still dispatch after lint passes; mode=%q", fake.calledMode)
	}
}

// TestLaneNoLintBypassesPreflight verifies the escape hatch: callers
// that already lint upstream (or want to test bypass) can pass
// --no-lint and the preflight is skipped entirely. The subcommand
// still dispatches normally.
func TestLaneNoLintBypassesPreflight(t *testing.T) {
	briefPath := writeBrief(t, "# Test brief\n")
	var lintCalls int32
	stubLaneBriefLint(t, func([]string, io.Writer, io.Writer) int {
		atomic.AddInt32(&lintCalls, 1)
		return 0
	})

	var stdout, stderr bytes.Buffer
	fake := &laneFakeRunner{}
	code := runLaneMainTest(t, []string{"plan-execute", "--no-lint", briefPath}, &stdout, &stderr, fake)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d (stderr=%s)", code, stderr.String())
	}
	if got := atomic.LoadInt32(&lintCalls); got != 0 {
		t.Fatalf("--no-lint must skip the preflight, got %d calls", got)
	}
}

// TestLaneAbortsOnLintFailure verifies the failure path: when lint
// returns non-zero, the lane wrapper propagates the exit code and
// does NOT dispatch the subcommand. The brief is unsafe; running it
// anyway would defeat the purpose of preflighting. Bypasses
// runLaneMainTest's auto-stub for the same reason as the test above.
func TestLaneAbortsOnLintFailure(t *testing.T) {
	briefPath := writeBrief(t, "# Bad brief\n")
	stubLaneBriefLint(t, func([]string, io.Writer, io.Writer) int { return 7 })

	var stdout, stderr bytes.Buffer
	fake := &laneFakeRunner{}
	cfg := config.Default()
	cfg.Workspace.EnableWrites = true
	deps := laneDeps{
		loadConfig:       func(string) (config.Config, error) { return cfg, nil },
		runPlanExecute:   fake.plan,
		runImplement:     fake.implement,
		defaultPromptRun: func(config.Config, string, runOptions, io.Writer) (string, error) { return "", nil },
	}
	code := runLaneMainWithDeps([]string{"plan-execute", briefPath}, &stdout, &stderr, deps)
	if code != 7 {
		t.Fatalf("lint exit must propagate; expected 7, got %d", code)
	}
	if fake.calledMode != "" {
		t.Fatalf("subcommand must NOT dispatch after lint failure; mode=%q", fake.calledMode)
	}
	if !strings.Contains(stderr.String(), "brief lint failed") {
		t.Fatalf("stderr should explain the abort: %s", stderr.String())
	}
}

// TestLaneSlotMutexAcquiresAndReleases verifies the Phase 6.5.2
// contract: a non-empty --slot key acquires a temp-dir mutex and
// releases it on exit. Two sequential dispatches with the same key
// must both succeed (release happens between them).
func TestLaneSlotMutexAcquiresAndReleases(t *testing.T) {
	briefPath := writeBrief(t, "# Test\n")
	var stdout, stderr bytes.Buffer

	fake1 := &laneFakeRunner{}
	if code := runLaneMainTest(t, []string{"plan-execute", "--slot", "phase6_5_test", briefPath}, &stdout, &stderr, fake1); code != 0 {
		t.Fatalf("first dispatch should acquire slot; got %d (stderr=%s)", code, stderr.String())
	}

	// The slot dir must be cleaned up; verify by checking the dir is gone.
	slotDir := filepath.Join(os.TempDir(), "idiom-lane-phase6_5_test")
	if _, err := os.Stat(slotDir); !os.IsNotExist(err) {
		t.Fatalf("slot dir should be removed after dispatch; stat err=%v", err)
	}

	fake2 := &laneFakeRunner{}
	if code := runLaneMainTest(t, []string{"plan-execute", "--slot", "phase6_5_test", briefPath}, &stdout, &stderr, fake2); code != 0 {
		t.Fatalf("second dispatch should re-acquire after release; got %d", code)
	}
}

// TestLaneSlotMutexRejectsHeldSlot verifies the held-slot path: if
// another process has the slot directory, a second acquire returns
// exit 3 with a clear "already held" message. Mirrors the behavior
// Colony's run_idiom_impl.sh implemented via mkdir's atomicity.
func TestLaneSlotMutexRejectsHeldSlot(t *testing.T) {
	briefPath := writeBrief(t, "# Test\n")

	// Pre-create the slot dir to simulate a concurrent dispatch
	// that already holds it.
	slotDir := filepath.Join(os.TempDir(), "idiom-lane-held_slot_test")
	if err := os.Mkdir(slotDir, 0o755); err != nil {
		t.Fatalf("pre-create slot: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(slotDir) })

	var stdout, stderr bytes.Buffer
	fake := &laneFakeRunner{}
	code := runLaneMainTest(t, []string{"plan-execute", "--slot", "held_slot_test", briefPath}, &stdout, &stderr, fake)
	if code != 3 {
		t.Fatalf("held slot must exit 3; got %d", code)
	}
	if !strings.Contains(stderr.String(), "already held") {
		t.Fatalf("stderr should explain the held slot: %s", stderr.String())
	}
	if fake.calledMode != "" {
		t.Fatalf("subcommand must NOT dispatch when slot is held; mode=%q", fake.calledMode)
	}
}

// TestAnalyzeLaneOutputDetectsReadOnlyMarker covers the first of
// the three regression classes Phase 6.5.3 lifts from Colony's
// wrapper: a write-capable brief routed to a read-only contract.
// Any of the three markers (verification_request,
// verification_only, conversation_only) in the output stream is a
// reliable degradation signal.
func TestAnalyzeLaneOutputDetectsReadOnlyMarker(t *testing.T) {
	out := []byte(`{"type":"route_plan","contract":"verification_only"}` + "\n" + `{"tool":"submit_plan"}`)
	deg := analyzeLaneOutput(out, 0)
	if len(deg) != 1 {
		t.Fatalf("expected exactly one degradation, got %d: %+v", len(deg), deg)
	}
	if deg[0].kind != "read_only_lane" {
		t.Fatalf("expected kind=read_only_lane, got %q", deg[0].kind)
	}
}

// TestAnalyzeLaneOutputDetectsNoEditAttempt covers the second
// regression class: the lane completed cleanly (exit 0) but never
// attempted an edit, never raised a `<<QUESTION>>:` blocker, and
// never emitted a typed completion_status=blocked event. This is
// the "silently ran without trying anything" path.
func TestAnalyzeLaneOutputDetectsNoEditAttempt(t *testing.T) {
	// Just some chatter, no edit tools, no blocker, no typed event.
	out := []byte(`{"type":"thinking","content":"musing about the brief"}` + "\n" + `{"type":"final_answer","content":"all done"}`)
	deg := analyzeLaneOutput(out, 0)
	if len(deg) != 1 {
		t.Fatalf("expected exactly one degradation, got %d: %+v", len(deg), deg)
	}
	if deg[0].kind != "no_edit_attempt" {
		t.Fatalf("expected kind=no_edit_attempt, got %q", deg[0].kind)
	}
}

// TestAnalyzeLaneOutputAcceptsEditAttempt verifies the positive
// path: any of the write-class tool calls in the output proves the
// lane reached an edit-capable conclusion. No degradation reported.
func TestAnalyzeLaneOutputAcceptsEditAttempt(t *testing.T) {
	for _, tool := range []string{"edit_file", "write_file", "append_file", "apply_patch", "submit_plan"} {
		out := []byte(`{"type":"tool_call","tool":"` + tool + `"}`)
		deg := analyzeLaneOutput(out, 0)
		if len(deg) != 0 {
			t.Fatalf("tool %q must satisfy the edit-attempt check; got %+v", tool, deg)
		}
	}
}

func TestAnalyzeLaneOutputAcceptsPlanExecuteTypedEditEvents(t *testing.T) {
	cases := []string{
		`{"type":"idiom.plan_execute.auto_exact_plan_submitted","edits":4}`,
		`{"type":"idiom.plan_execute.executor_edit","path":"a.go","status":"ok"}`,
		`{"type":"idiom.plan_execute.executor_complete","total":4,"succeeded":4,"failed":0}`,
	}
	for _, line := range cases {
		deg := analyzeLaneOutput([]byte(line), 0)
		if len(deg) != 0 {
			t.Fatalf("plan-execute typed edit event must satisfy the edit-attempt check; event=%s got %+v", line, deg)
		}
	}
}

// TestAnalyzeLaneOutputAcceptsTypedBlocker covers the typed-blocker
// path: a completion_status=blocked event proves the lane reached a
// defined stopping point (tool-step budget exhausted, write guard
// rejected, etc.) — counts as a legitimate completion, not a
// silent no-op.
func TestAnalyzeLaneOutputAcceptsTypedBlocker(t *testing.T) {
	out := []byte(`{"type":"completion_status","status":"blocked","reason":"tool_step_budget_exhausted"}`)
	deg := analyzeLaneOutput(out, 0)
	if len(deg) != 0 {
		t.Fatalf("typed blocked completion must satisfy the check; got %+v", deg)
	}
}

// TestAnalyzeLaneOutputAcceptsQuestionBlocker covers the
// `<<QUESTION>>:` path: a typed text blocker also counts as the
// lane reaching a defined stopping point (operator-visible
// blocker), distinct from silent no-op exits.
func TestAnalyzeLaneOutputAcceptsQuestionBlocker(t *testing.T) {
	out := []byte("model output line\n<<QUESTION>>: what is the canonical anchor?\n")
	deg := analyzeLaneOutput(out, 0)
	if len(deg) != 0 {
		t.Fatalf("<<QUESTION>>: blocker must satisfy the check; got %+v", deg)
	}
}

func TestAnalyzeLaneOutputAcceptsSourceSatisfiedEvent(t *testing.T) {
	out := []byte(`{"type":"final","content":"Decision: source-satisfied"}` + "\n" + `{"type":"idiom.plan_execute.source_satisfied","required_edit_paths":["a.go"]}`)
	deg := analyzeLaneOutput(out, 0)
	if len(deg) != 0 {
		t.Fatalf("source-satisfied event must satisfy the check; got %+v", deg)
	}
	if got := laneRunFinalState(0, deg, out); got != "source-satisfied" {
		t.Fatalf("source-satisfied event should be recorded in lane history, got %q", got)
	}
}

func TestLaneHistoryRecordsReviewRepairDegradedAfterExecutorEdit(t *testing.T) {
	out := []byte(strings.Join([]string{
		`{"type":"idiom.plan_execute.executor_edit","path":"a.go","status":"applied"}`,
		`{"type":"idiom.plan_execute.review_repair_degraded","status":"review_unavailable","reason_class":"backend_eof"}`,
	}, "\n"))
	deg := analyzeLaneOutput(out, 0)
	if len(deg) != 0 {
		t.Fatalf("review-repair availability degradation after an edit must not become a lane regression: %+v", deg)
	}
	if got := laneRunFinalState(0, deg, out); got != "degraded" {
		t.Fatalf("review-repair degraded event should be recorded in lane history, got %q", got)
	}
}

// TestAnalyzeLaneOutputSkipsNoEditWhenSubcommandFailed verifies the
// noise-suppression rule: when the subcommand exits non-zero, the
// "no edit attempt" check is skipped because the non-zero exit
// already signals failure. Stacking the no-edit degradation on top
// would just add noise to a wrapper that's already going to exit
// with the subcommand's code.
func TestAnalyzeLaneOutputSkipsNoEditWhenSubcommandFailed(t *testing.T) {
	out := []byte(`{"type":"final_answer","content":"crashed"}`)
	deg := analyzeLaneOutput(out, 1)
	for _, d := range deg {
		if d.kind == "no_edit_attempt" {
			t.Fatalf("no_edit_attempt must be suppressed when subcommand failed; got %+v", deg)
		}
	}
}

// TestRunLaneMainEmitsDegradedEventOnReadOnlyLane is the end-to-end
// integration: a fake subcommand that prints a read-only marker
// produces the structured `idiom.lane.degraded` event on stdout
// and the wrapper exits with code 4 (degraded). This is the
// signal-emission path Colony's wrapper subscribes to.
func TestRunLaneMainEmitsDegradedEventOnReadOnlyLane(t *testing.T) {
	briefPath := writeBrief(t, "# Test\n")
	stubLaneBriefLint(t, func([]string, io.Writer, io.Writer) int { return 0 })

	plan := func(args []string, stdout io.Writer, _ io.Writer, _ promptRunner) int {
		_, _ = stdout.Write([]byte(`{"type":"route_plan","contract":"verification_only"}` + "\n"))
		return 0
	}
	implement := func([]string, io.Writer, io.Writer, promptRunner) int { return 0 }

	var stdout, stderr bytes.Buffer
	code := runLaneMainWithCustomRunners(t, []string{"plan-execute", briefPath}, &stdout, &stderr, plan, implement)
	if code != 4 {
		t.Fatalf("read-only lane must surface as exit 4, got %d (stderr=%s)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"type":"idiom.lane.degraded"`) {
		t.Fatalf("expected idiom.lane.degraded event on stdout: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), `"kind":"read_only_lane"`) {
		t.Fatalf("event should carry the kind=read_only_lane signal: %s", stdout.String())
	}
}

// runLaneMainWithCustomRunners is a Phase 6.5-specific helper that
// lets tests inject arbitrary plan / implement runners (vs the
// canonical laneFakeRunner pattern that just records calls). Used
// when a test needs the runner to actually produce stdout the
// post-analyzer can scan.
func runLaneMainWithCustomRunners(t *testing.T, args []string, stdout, stderr io.Writer,
	plan, implement laneSubcommandRunner) int {
	t.Helper()
	stubLaneBriefLint(t, func([]string, io.Writer, io.Writer) int { return 0 })
	cfg := config.Default()
	cfg.Workspace.EnableWrites = true
	deps := laneDeps{
		loadConfig:       func(string) (config.Config, error) { return cfg, nil },
		runPlanExecute:   plan,
		runImplement:     implement,
		defaultPromptRun: func(config.Config, string, runOptions, io.Writer) (string, error) { return "", nil },
	}
	return runLaneMainWithDeps(args, stdout, stderr, deps)
}

func TestLanePostflightFlagRunsAfterDispatch(t *testing.T) {
	briefPath := writeBrief(t, "# Test\n")
	stubLaneBriefLint(t, func([]string, io.Writer, io.Writer) int { return 0 })

	// Stub git diff so the test doesn't need an actual git repo in
	// the temp workspace; an empty diff is the postflight input.
	origGitDiff := laneGitDiff
	laneGitDiff = func(string) ([]byte, error) { return []byte(""), nil }
	t.Cleanup(func() { laneGitDiff = origGitDiff })

	var postflightCalls int32
	stubLanePostflightRunner(t, func([]string, io.Writer, io.Writer) int {
		atomic.AddInt32(&postflightCalls, 1)
		return 3
	})

	fake := &laneFakeRunner{}
	cfg := config.Default()
	cfg.Workspace.EnableWrites = true
	deps := laneDeps{
		loadConfig:       func(string) (config.Config, error) { return cfg, nil },
		runPlanExecute:   fake.plan,
		runImplement:     fake.implement,
		defaultPromptRun: func(config.Config, string, runOptions, io.Writer) (string, error) { return "", nil },
	}
	var stdout, stderr bytes.Buffer
	code := runLaneMainWithDeps([]string{"plan-execute", "--postflight", briefPath}, &stdout, &stderr, deps)
	if got := atomic.LoadInt32(&postflightCalls); got != 1 {
		t.Fatalf("postflight should run exactly once when --postflight is set; got %d", got)
	}
	if code != 3 {
		t.Fatalf("max(lane=0, postflight=3) should be 3; got %d", code)
	}
}

func TestLanePostflightFlagPassesBrief(t *testing.T) {
	briefPath := writeBrief(t, "# Test\n")
	stubLaneBriefLint(t, func([]string, io.Writer, io.Writer) int { return 0 })

	origGitDiff := laneGitDiff
	laneGitDiff = func(string) ([]byte, error) { return []byte(""), nil }
	t.Cleanup(func() { laneGitDiff = origGitDiff })

	var capturedArgs []string
	stubLanePostflightRunner(t, func(args []string, _, _ io.Writer) int {
		capturedArgs = append([]string(nil), args...)
		return 0
	})

	fake := &laneFakeRunner{}
	cfg := config.Default()
	cfg.Workspace.EnableWrites = true
	deps := laneDeps{
		loadConfig:       func(string) (config.Config, error) { return cfg, nil },
		runPlanExecute:   fake.plan,
		runImplement:     fake.implement,
		defaultPromptRun: func(config.Config, string, runOptions, io.Writer) (string, error) { return "", nil },
	}
	var stdout, stderr bytes.Buffer
	runLaneMainWithDeps([]string{"plan-execute", "--postflight", briefPath}, &stdout, &stderr, deps)

	sawBriefFlag := false
	sawBriefPath := false
	for i, a := range capturedArgs {
		if a == "--brief" && i+1 < len(capturedArgs) && capturedArgs[i+1] == briefPath {
			sawBriefFlag = true
			sawBriefPath = true
			break
		}
	}
	if !sawBriefFlag || !sawBriefPath {
		t.Fatalf("postflight should receive --brief %s; got args: %v", briefPath, capturedArgs)
	}
}
