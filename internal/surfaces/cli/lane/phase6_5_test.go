package lane

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"idiom/internal/capabilities/workspace"
	lanecontract "idiom/internal/contracts/lane"
	"idiom/internal/evidence/telemetry"
	"idiom/internal/platform/config"
	cmdruntime "idiom/internal/surfaces/cli/runtime"
	"idiom/internal/workflows/planexecute"
	planexecuterunner "idiom/internal/workflows/planexecute/runner"
)

var _ = ginkgo.Describe("phase6 5", func() {
	ginkgo.It("lane runs brief lint by default", func() {
		briefPath := writeBrief("# Test brief\n")

		var lintCalls int32

		stubLaneBriefLint(laneBriefLintStub(func(path string) int {
			atomic.AddInt32(&lintCalls, 1)
			gomega.Expect(path).To(gomega.Equal(briefPath), fmt.Sprintf("lint runner expected %q, got %q", briefPath, path))

			return 0
		}))

		var stdout, stderr bytes.Buffer

		fake := &laneFakeRunner{}
		cfg := config.Default()

		cfg.Workspace.EnableWrites = true

		deps := laneDeps{
			loadConfig:       func(string) (config.Config, error) { return cfg, nil },
			runPlanExecute:   fake.plan,
			runImplement:     fake.implement,
			defaultPromptRun: func(config.Config, string, cmdruntime.RunOptions, io.Writer) (string, error) { return "", nil },
		}
		code := runLaneMainWithDeps([]string{"plan-execute", briefPath}, &stdout, &stderr, deps)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("expected exit 0, got %d (stderr=%s)", code, stderr.String()))

		if got := atomic.LoadInt32(&lintCalls); got != 1 {
			ginkgo.Fail(fmt.Sprintf("expected lint to run exactly once, got %d calls", got))
		}
		gomega.Expect(fake.calledMode).To(gomega.Equal("plan-execute"), fmt.Sprintf("subcommand should still dispatch after lint passes; mode=%q", fake.calledMode))

	})
	ginkgo.It("lane no lint bypasses preflight", func() {
		briefPath := writeBrief("# Test brief\n")

		var lintCalls int32

		stubLaneBriefLint(laneBriefLintStub(func(string) int {
			atomic.AddInt32(&lintCalls, 1)

			return 0
		}))

		var stdout, stderr bytes.Buffer

		fake := &laneFakeRunner{}
		code := runLaneMainTest([]string{"plan-execute", "--no-lint", briefPath}, &stdout, &stderr, fake)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("expected exit 0, got %d (stderr=%s)", code, stderr.String()))

		if got := atomic.LoadInt32(&lintCalls); got != 0 {
			ginkgo.Fail(fmt.Sprintf("--no-lint must skip the preflight, got %d calls", got))
		}
	})
	ginkgo.It("lane aborts on lint failure", func() {
		briefPath := writeBrief("# Bad brief\n")
		stubLaneBriefLint(laneBriefLintStub(func(string) int { return 7 }))

		var stdout, stderr bytes.Buffer

		fake := &laneFakeRunner{}
		cfg := config.Default()

		cfg.Workspace.EnableWrites = true

		deps := laneDeps{
			loadConfig:       func(string) (config.Config, error) { return cfg, nil },
			runPlanExecute:   fake.plan,
			runImplement:     fake.implement,
			defaultPromptRun: func(config.Config, string, cmdruntime.RunOptions, io.Writer) (string, error) { return "", nil },
		}
		code := runLaneMainWithDeps([]string{"plan-execute", briefPath}, &stdout, &stderr, deps)
		gomega.Expect(code).To(gomega.Equal(7), fmt.Sprintf("lint exit must propagate; expected 7, got %d", code))
		gomega.Expect(fake.calledMode).To(gomega.Equal(""), fmt.Sprintf("subcommand must NOT dispatch after lint failure; mode=%q", fake.calledMode))
		gomega.Expect(stderr.String()).To(gomega.ContainSubstring("brief lint failed"), fmt.Sprintf("stderr should explain the abort: %s", stderr.String()))

	})
	ginkgo.It("lane slot mutex acquires and releases", func() {
		briefPath := writeBrief("# Test\n")

		var stdout, stderr bytes.Buffer

		fake1 := &laneFakeRunner{}
		if code := runLaneMainTest([]string{"plan-execute", "--slot", "phase6_5_test", briefPath}, &stdout, &stderr, fake1); code != 0 {
			ginkgo.Fail(fmt.Sprintf("first dispatch should acquire slot; got %d (stderr=%s)", code, stderr.String()))
		}

		slotDir := filepath.Join(os.TempDir(), "idiom-lane-phase6_5_test")
		if _, err := os.Stat(slotDir); !os.IsNotExist(err) {
			ginkgo.Fail(fmt.Sprintf("slot dir should be removed after dispatch; stat err=%v", err))
		}

		fake2 := &laneFakeRunner{}
		if code := runLaneMainTest([]string{"plan-execute", "--slot", "phase6_5_test", briefPath}, &stdout, &stderr, fake2); code != 0 {
			ginkgo.Fail(fmt.Sprintf("second dispatch should re-acquire after release; got %d", code))
		}
	})
	ginkgo.It("lane slot mutex rejects held slot", func() {
		briefPath := writeBrief("# Test\n")

		slotDir := filepath.Join(os.TempDir(), "idiom-lane-held_slot_test")
		gomega.Expect(os.Mkdir(slotDir, 0o755)).To(gomega.Succeed())
		ginkgo.DeferCleanup(func() { _ = os.Remove(slotDir) })

		var stdout, stderr bytes.Buffer

		fake := &laneFakeRunner{}
		code := runLaneMainTest([]string{"plan-execute", "--slot", "held_slot_test", briefPath}, &stdout, &stderr, fake)
		gomega.Expect(code).To(gomega.Equal(3), fmt.Sprintf("held slot must exit 3; got %d", code))
		gomega.Expect(stderr.String()).To(gomega.ContainSubstring("already held"), fmt.Sprintf("stderr should explain the held slot: %s", stderr.String()))
		gomega.Expect(fake.calledMode).To(gomega.Equal(""), fmt.Sprintf("subcommand must NOT dispatch when slot is held; mode=%q", fake.calledMode))

	})
	ginkgo.It("analyze lane output detects read only marker", func() {
		out := []byte(`{"type":"route_plan","contract":"verification_only"}` + "\n" + `{"tool":"submit_plan"}`)
		facts := analyzeLaneOutputFacts(out)
		deg := facts.degradations(0)
		gomega.Expect(deg).To(gomega.HaveLen(1), fmt.Sprintf("expected exactly one degradation, got %d: %+v", len(deg), deg))
		gomega.Expect(deg[0].kind).To(gomega.Equal("read_only_lane"), fmt.Sprintf("expected kind=read_only_lane, got %q", deg[0].kind))

	})
	ginkgo.It("analyze lane output detects no edit attempt", func() {

		out := []byte(`{"type":"thinking","content":"musing about the brief"}` + "\n" + `{"type":"final_answer","content":"all done"}`)
		facts := analyzeLaneOutputFacts(out)
		deg := facts.degradations(0)
		gomega.Expect(deg).To(gomega.HaveLen(1), fmt.Sprintf("expected exactly one degradation, got %d: %+v", len(deg), deg))
		gomega.Expect(deg[0].kind).To(gomega.Equal("no_edit_attempt"), fmt.Sprintf("expected kind=no_edit_attempt, got %q", deg[0].kind))

	})
	ginkgo.It("analyze lane output accepts edit attempt", func() {
		for _, tool := range []string{
			workspace.ToolNameEditFile,
			workspace.ToolNameWriteFile,
			workspace.ToolNameAppendFile,
			workspace.ToolNameApplyPatch,
			workspace.ToolNameMkdir,
			workspace.ToolNameGoMoveDeclarations,
			workspace.ToolNameSubmitPlan,
		} {
			out := []byte(`{"type":"tool_call","tool":"` + tool + `"}`)
			facts := analyzeLaneOutputFacts(out)
			deg := facts.degradations(0)
			gomega.Expect(deg).To(gomega.BeEmpty(), fmt.Sprintf("tool %q must satisfy the edit-attempt check; got %+v", tool, deg))

		}
	})
	ginkgo.It("analyze lane output accepts plan execute typed edit events", func() {
		cases := []string{
			`{"type":"idiom.plan_execute.auto_exact_plan_submitted","edits":4}`,
			`{"type":"idiom.plan_execute.executor_edit","path":"a.go","status":"ok"}`,
			`{"type":"idiom.plan_execute.executor_complete","total":4,"succeeded":4,"failed":0}`,
		}
		for _, line := range cases {
			facts := analyzeLaneOutputFacts([]byte(line))
			deg := facts.degradations(0)
			gomega.Expect(deg).To(gomega.BeEmpty(), fmt.Sprintf("plan-execute typed edit event must satisfy the edit-attempt check; event=%s got %+v", line, deg))

		}
	})
	ginkgo.It("analyze lane output accepts typed blocker", func() {
		out := []byte(`{"type":"completion_status","status":"blocked","reason":"tool_step_budget_exhausted"}`)
		facts := analyzeLaneOutputFacts(out)
		deg := facts.degradations(0)
		gomega.Expect(deg).To(gomega.BeEmpty(), fmt.Sprintf("typed blocked completion must satisfy the check; got %+v", deg))

	})
	ginkgo.It("analyze lane output accepts question blocker", func() {
		out := []byte("model output line\n<<QUESTION>>: what is the canonical anchor?\n")
		facts := analyzeLaneOutputFacts(out)
		deg := facts.degradations(0)
		gomega.Expect(deg).To(gomega.BeEmpty(), fmt.Sprintf("<<QUESTION>>: blocker must satisfy the check; got %+v", deg))

	})
	ginkgo.It("analyze lane output accepts source satisfied event", func() {
		out := []byte(`{"type":"final","content":"Decision: source-satisfied"}` + "\n" + `{"type":"idiom.plan_execute.source_satisfied","required_edit_paths":["a.go"]}`)
		facts := analyzeLaneOutputFacts(out)
		deg := facts.degradations(0)
		gomega.Expect(deg).To(gomega.BeEmpty(), fmt.Sprintf("source-satisfied event must satisfy the check; got %+v", deg))

		if got := laneRunFinalState(0, deg, facts); got != "source-satisfied" {
			ginkgo.Fail(fmt.Sprintf("source-satisfied event should be recorded in lane history, got %q", got))
		}
	})
	ginkgo.It("lane history records review repair degraded after executor edit", func() {
		out := []byte(strings.Join([]string{
			`{"type":"idiom.plan_execute.executor_edit","path":"a.go","status":"applied"}`,
			`{"type":"idiom.plan_execute.review_repair_degraded","status":"degraded","reason_class":"backend_eof","status_detail":"review unavailable"}`,
		}, "\n"))
		facts := analyzeLaneOutputFacts(out)
		deg := facts.degradations(0)
		gomega.Expect(deg).To(gomega.BeEmpty(), fmt.Sprintf("review-repair availability degradation after an edit must not become a lane regression: %+v", deg))

		if got := laneRunFinalState(0, deg, facts); got != "degraded" {
			ginkgo.Fail(fmt.Sprintf("review-repair degraded event should be recorded in lane history, got %q", got))
		}
	})
	ginkgo.It("analyze lane output skips no edit when subcommand failed", func() {
		out := []byte(`{"type":"final_answer","content":"crashed"}`)
		facts := analyzeLaneOutputFacts(out)
		deg := facts.degradations(1)

		for _, d := range deg {
			gomega.Expect(d.kind).NotTo(gomega.Equal("no_edit_attempt"), fmt.Sprintf("no_edit_attempt must be suppressed when subcommand failed; got %+v", deg))

		}
	})
	ginkgo.It("run lane main emits degraded event on read only lane", func() {
		briefPath := writeBrief("# Test\n")
		stubLaneBriefLint(passingLaneBriefLint)

		plan := func(_ config.Config, _ planexecute.Options, _ planexecuterunner.Options, _ planexecute.PromptRunner, _ *telemetry.Recorder, stdout io.Writer, _ io.Writer) planexecute.RunResult {
			_, _ = stdout.Write([]byte(`{"type":"route_plan","contract":"verification_only"}` + "\n"))
			return planexecute.RunResult{ExitCode: 0}
		}
		implement := func([]string, io.Writer, io.Writer, cmdruntime.PromptRunner) int { return 0 }

		var stdout, stderr bytes.Buffer

		code := runLaneMainWithCustomRunners([]string{"plan-execute", briefPath}, &stdout, &stderr, plan, implement)
		gomega.Expect(code).To(gomega.Equal(4), fmt.Sprintf("read-only lane must surface as exit 4, got %d (stderr=%s)", code, stderr.String()))
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring(`"type":"`+lanecontract.EventName(lanecontract.EventDegraded)+`"`), fmt.Sprintf("expected idiom.lane.degraded event on stdout: %s", stdout.String()))
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring(`"kind":"read_only_lane"`), fmt.Sprintf("event should carry the kind=read_only_lane signal: %s", stdout.String()))

	})
	ginkgo.It("lane postflight flag runs after dispatch", func() {
		briefPath := writeBrief("# Test\n")
		stubLaneBriefLint(passingLaneBriefLint)

		origGitDiff := laneGitDiff

		laneGitDiff = func(string) ([]byte, error) { return []byte(""), nil }
		ginkgo.DeferCleanup(func() { laneGitDiff = origGitDiff })

		var postflightCalls int32

		stubLanePostflightRunner(func([]string, io.Writer, io.Writer) int {
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
			defaultPromptRun: func(config.Config, string, cmdruntime.RunOptions, io.Writer) (string, error) { return "", nil },
		}

		var stdout, stderr bytes.Buffer

		code := runLaneMainWithDeps([]string{"plan-execute", "--postflight", briefPath}, &stdout, &stderr, deps)

		if got := atomic.LoadInt32(&postflightCalls); got != 1 {
			ginkgo.Fail(fmt.Sprintf("postflight should run exactly once when --postflight is set; got %d", got))
		}
		gomega.Expect(code).To(gomega.Equal(3), fmt.Sprintf("max(lane=0, postflight=3) should be 3; got %d", code))

	})
	ginkgo.It("lane postflight flag passes brief", func() {
		briefPath := writeBrief("# Test\n")
		stubLaneBriefLint(passingLaneBriefLint)

		origGitDiff := laneGitDiff

		laneGitDiff = func(string) ([]byte, error) { return []byte(""), nil }
		ginkgo.DeferCleanup(func() { laneGitDiff = origGitDiff })

		var capturedArgs []string

		stubLanePostflightRunner(func(args []string, _, _ io.Writer) int {
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
			defaultPromptRun: func(config.Config, string, cmdruntime.RunOptions, io.Writer) (string, error) { return "", nil },
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
		gomega.Expect(sawBriefFlag).To(gomega.BeTrue(), fmt.Sprintf("postflight should receive --brief %s; got args: %v", briefPath, capturedArgs))
		gomega.Expect(sawBriefPath).To(gomega.BeTrue(), fmt.Sprintf("postflight should receive --brief %s; got args: %v", briefPath, capturedArgs))

	})
})

func runLaneMainWithCustomRunners(args []string, stdout, stderr io.Writer,
	plan lanePlanExecuteRunner, implement laneImplementRunner) int {

	stubLaneBriefLint(passingLaneBriefLint)

	cfg := config.Default()

	cfg.Workspace.EnableWrites = true

	deps := laneDeps{
		loadConfig:       func(string) (config.Config, error) { return cfg, nil },
		runPlanExecute:   plan,
		runImplement:     implement,
		defaultPromptRun: func(config.Config, string, cmdruntime.RunOptions, io.Writer) (string, error) { return "", nil },
	}

	return runLaneMainWithDeps(args, stdout, stderr, deps)
}
