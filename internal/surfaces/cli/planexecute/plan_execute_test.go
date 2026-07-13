package planexecute

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	"idiom/internal/contracts/autonomy"
	planartifact "idiom/internal/contracts/planartifact"

	"idiom/internal/capabilities/workspace"
	intentloopcontract "idiom/internal/contracts/intentloop"
	plancontract "idiom/internal/contracts/plan"
	"idiom/internal/evidence/telemetry"
	"idiom/internal/platform/config"
	"idiom/internal/platform/jsonevents/jsoneventstest"
	"idiom/internal/repository/comprehension"
	"idiom/internal/runtime/runner"
	runtimesession "idiom/internal/runtime/session"
	cmdruntime "idiom/internal/surfaces/cli/runtime"
	"idiom/internal/surfaces/tui/app"
	"idiom/internal/workflows/intentloop"
	"idiom/internal/workflows/planexecute"
	planexecuteadapters "idiom/internal/workflows/planexecute/adapters"
	planexecuterunner "idiom/internal/workflows/planexecute/runner"
)

var _ = ginkgo.It("run plan execute main forwards thinking and max tokens", func() {
	configPath := writePlanExecuteConfig()

	var (
		stdout, stderr bytes.Buffer
		gotOpts        cmdruntime.RunOptions
	)

	code := RunMainWithTelemetry(
		[]string{"-config", configPath, "-prompt", "plan this", "-no-thinking", "-max-tokens", "2048"},
		&stdout,
		&stderr,
		func(_ config.Config, _ string, opts cmdruntime.RunOptions, _ io.Writer) (string, error) {
			gotOpts = opts
			return "no plan", nil
		},
		nil,
	)
	gomega.Expect(code).To(gomega.Equal(1), fmt.Sprintf("expected planner-without-artifact exit 1, got %d stderr=%s", code, stderr.String()))
	gomega.Expect(gotOpts.DisableThinking).To(gomega.BeTrue(), "--no-thinking should reach the plan-execute planner run")
	gomega.Expect(gotOpts.MaxTokensOverride).To(gomega.Equal(2048), fmt.Sprintf("MaxTokensOverride = %d, want 2048", gotOpts.MaxTokensOverride))

})
var _ = ginkgo.It("run plan execute main forwards typed authorized new files", func() {
	configPath := writePlanExecuteConfig()

	var (
		stdout, stderr bytes.Buffer
		gotOpts        cmdruntime.RunOptions
	)

	code := RunMainWithTelemetry(
		[]string{"-config", configPath, "-prompt", "create pkg/new_helper.go", "-authorized-new-file", "pkg/new_helper.go"},
		&stdout,
		&stderr,
		func(_ config.Config, _ string, opts cmdruntime.RunOptions, _ io.Writer) (string, error) {
			gotOpts = opts
			return "no plan", nil
		},
		nil,
	)
	gomega.Expect(code).To(gomega.Equal(1), fmt.Sprintf("expected planner-without-artifact exit 1, got %d stderr=%s", code, stderr.String()))
	gomega.Expect(slices.Equal(gotOpts.AuthorizedNewFiles, []string{"pkg/new_helper.go"})).To(gomega.BeTrue(), fmt.Sprintf("AuthorizedNewFiles = %#v, want pkg/new_helper.go", gotOpts.AuthorizedNewFiles))

})
var _ = ginkgo.It("run plan execute main default does not route intent loop", func() {
	configPath := writePlanExecuteConfig()

	var stdout, stderr bytes.Buffer

	code := RunMainWithTelemetry(
		[]string{"-config", configPath, "-prompt", "plan this"},
		&stdout,
		&stderr,
		func(config.Config, string, cmdruntime.RunOptions, io.Writer) (string, error) {
			return "no plan", nil
		},
		nil,
	)
	gomega.Expect(code).To(gomega.Equal(1), fmt.Sprintf("expected normal plan-execute missing-artifact exit 1, got %d stderr=%s", code, stderr.String()))

})
var _ = ginkgo.It("parse plan execute intent loop flags", func() {
	configPath := writePlanExecuteConfig()

	var stderr bytes.Buffer
	gotOpts, gotLoopOpts, err := parsePlanExecuteCLIOptions([]string{
		"-config", configPath,
		"-prompt", "loop this",
		"-intent-loop",
		"-max-cycles", "3",
		"-total-budget-tokens", "12000",
	}, &stderr)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("parse intent-loop flags: %v stderr=%s", err, stderr.String()))
	gomega.Expect(gotOpts.Prompt).To(gomega.Equal("loop this"), fmt.Sprintf("prompt = %q, want loop this", gotOpts.Prompt))
	gomega.Expect(gotLoopOpts.IntentLoop).To(gomega.BeTrue(), fmt.Sprintf("loop opts = %#v, want enabled max=3 budget=12000", gotLoopOpts))
	gomega.Expect(gotLoopOpts.MaxCycles).To(gomega.Equal(3), fmt.Sprintf("loop opts = %#v, want enabled max=3 budget=12000", gotLoopOpts))
	gomega.Expect(gotLoopOpts.TotalBudgetTokens).To(gomega.Equal(12000), fmt.Sprintf("loop opts = %#v, want enabled max=3 budget=12000", gotLoopOpts))

})
var _ = ginkgo.It("run plan execute main intent loop invalid budget exits2", func() {
	configPath := writePlanExecuteConfig()

	var stdout, stderr bytes.Buffer

	code := RunMainWithTelemetry(
		[]string{"-config", configPath, "-prompt", "loop this", "-intent-loop", "-total-budget-tokens", "-1"},
		&stdout,
		&stderr,
		func(config.Config, string, cmdruntime.RunOptions, io.Writer) (string, error) {
			ginkgo.Fail("prompt runner must not be called after invalid intent-loop budget")
			return "", nil
		},
		nil,
	)
	gomega.Expect(code).To(gomega.Equal(2), fmt.Sprintf("expected invalid budget exit 2, got %d stderr=%s", code, stderr.String()))
	gomega.Expect(stderr.String()).To(gomega.ContainSubstring("--total-budget-tokens"), fmt.Sprintf("stderr should name invalid budget flag, got %s", stderr.String()))

})

var _ = ginkgo.Describe("Plan-execute intent loop", ginkgo.Label("e2e", "scripted"), func() {
	ginkgo.It("runs a scripted live loop without leaking the raw prompt", func() {
		t := ginkgo.GinkgoT()
		root := t.TempDir()
		socket := filepath.Join("/tmp", "idiom-"+strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())+".sock")
		_ = os.Remove(socket)

		srv := runner.NewServer(runner.Config{Pools: map[string]runner.PoolConfig{"qwen-35b": {MaxInFlight: 1}}}, socket)
		gomega.Expect(srv.Start()).To(gomega.Succeed())
		ginkgo.DeferCleanup(func() {
			srv.Stop()
			_ = os.Remove(socket)
		})
		t.Setenv("IDIOM_QUEUE_POOL", "qwen-35b")
		t.Setenv("IDIOM_QUEUE_SOCKET", socket)

		configPath := filepath.Join(root, "config.json")
		configBody := fmt.Sprintf(`{"workspace":{"root":%q,"cwd":%q}}`, root, root)
		gomega.Expect(os.WriteFile(configPath, []byte(configBody), 0o644)).To(gomega.Succeed())

		prompt := `Scripted live e2e secret marker: RAW_PROMPT_SHOULD_NOT_LEAK.

Typed plan execution contract:
{
  "atoms": [{
    "atom_id": "impl",
    "scope": {
      "required_coverage_paths": ["internal/intentloop_e2e_target.go"]
    },
    "required_tests": [
      {"id": "unit", "command": "go test ./internal/intentloop-e2e/..."}
    ],
    "acceptance_obligations": [
      {"id": "coverage-threshold", "description": "coverage >= 80%", "required": true, "threshold": ">=80%"}
    ],
    "terminal_evidence_requirements": {
      "explicit": true,
      "require_verification": true,
      "require_required_tests": true
    }
  }]
}`

		var (
			childSessions []string
			childPrompts  []string
		)
		runOpts := planexecuterunner.Options{IntentLoop: true, MaxCycles: 4}
		runOpts.ChildRunner = func(_ config.Config, opts planexecute.Options, _ planexecute.PromptRunner, _ *telemetry.Recorder, _ io.Writer, _ io.Writer) planexecute.RunResult {
			childSessions = append(childSessions, opts.SessionID)
			childPrompts = append(childPrompts, opts.Prompt)

			gomega.Expect(opts.ScopeExtractionPolicy).To(gomega.Equal(planexecute.ScopeExtractionFrozen))
			gomega.Expect(opts.ScopeContract.RequiredCoveragePaths).To(gomega.Equal([]string{"internal/intentloop_e2e_target.go"}))
			gomega.Expect(opts.VerificationPlanExplicitCommands).To(gomega.Equal([]string{"go test ./internal/intentloop-e2e/..."}))

			cycle := len(childSessions)
			record := scriptedIntentLoopTerminal(opts.SessionID, cycle)
			exitCode := 1
			if cycle == 3 {
				exitCode = 0
			}

			return planexecute.RunResult{
				ExitCode:       exitCode,
				SessionID:      opts.SessionID,
				Completion:     &record,
				CompletionPath: ".idiom/plan-execute/sessions/" + opts.SessionID + ".terminal.json",
			}
		}

		cfg, err := config.Load(configPath)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		var stdout, stderr bytes.Buffer
		result := planexecuterunner.Run(
			cfg,
			planexecute.Options{ConfigPath: configPath, Prompt: prompt, JSONEvents: true},
			runOpts,
			AdaptPromptRunner(func(config.Config, string, cmdruntime.RunOptions, io.Writer) (string, error) {
				ginkgo.Fail("scripted child runner should avoid live model calls")
				return "", nil
			}),
			nil,
			&stdout,
			&stderr,
		)

		ginkgo.GinkgoWriter.Printf("scripted intent-loop stdout:\n%s", stdout.String())
		ginkgo.GinkgoWriter.Printf("scripted intent-loop stderr:\n%s", stderr.String())

		gomega.Expect(result.ExitCode).To(gomega.BeZero(), "stdout=%s stderr=%s", stdout.String(), stderr.String())
		gomega.Expect(childSessions).To(gomega.HaveLen(3))
		gomega.Expect(childPrompts).To(gomega.HaveLen(3))
		gomega.Expect(childPrompts[1]).To(gomega.ContainSubstring("verification:go test ./internal/intentloop-e2e/..."))
		gomega.Expect(childPrompts[2]).To(gomega.And(
			gomega.ContainSubstring("acceptance:coverage-threshold"),
			gomega.ContainSubstring("threshold_result: `not_met`"),
		))

		var events []map[string]any
		for _, line := range strings.Split(stdout.String(), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || !strings.HasPrefix(line, "{") {
				continue
			}

			var event map[string]any
			gomega.Expect(json.Unmarshal([]byte(line), &event)).To(gomega.Succeed())
			events = append(events, event)
		}
		_, foundStart := jsoneventstest.FindByType(events, intentloopcontract.EventName(intentloopcontract.EventStart))
		gomega.Expect(foundStart).To(gomega.BeTrue(), stdout.String())

		complete, foundComplete := jsoneventstest.FindByType(events, intentloopcontract.EventName(intentloopcontract.EventComplete))
		gomega.Expect(foundComplete).To(gomega.BeTrue(), stdout.String())
		gomega.Expect(complete["total_cycles"]).To(gomega.Equal(float64(3)))
		gomega.Expect(stdout.String()).NotTo(gomega.ContainSubstring("RAW_PROMPT_SHOULD_NOT_LEAK"))

		stateFiles, err := filepath.Glob(filepath.Join(root, ".idiom", "intent-loop", "sessions", "*.json"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(stateFiles).To(gomega.HaveLen(1))

		stateID := strings.TrimSuffix(filepath.Base(stateFiles[0]), ".json")
		state, _, err := intentloop.LoadState(root, stateID)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(state.Status).To(gomega.Equal(intentloopcontract.StatusCompleted))
		gomega.Expect(state.Cycles).To(gomega.HaveLen(3))
		gomega.Expect(state.Cycles[0].RemainingFailures).NotTo(gomega.BeEmpty())
		gomega.Expect(state.Cycles[1].RemainingFailures).NotTo(gomega.BeEmpty())
		gomega.Expect(state.Cycles[2].RemainingFailures).To(gomega.BeEmpty())
		gomega.Expect(state.Cycles[0].CyclePromptSHA256).NotTo(gomega.BeEmpty())
		gomega.Expect(state.Cycles[1].CyclePromptPath).NotTo(gomega.BeEmpty())
		gomega.Expect(state.Cycles[2].ChildIdentity).NotTo(gomega.BeNil())
	})
})
var _ = ginkgo.It("run plan execute main delegates planner options and model override", func() {
	configPath := writePlanExecuteConfig()

	var (
		stdout, stderr bytes.Buffer
		gotCfg         config.Config
		gotOpts        cmdruntime.RunOptions
		gotPrompt      string
	)

	code := RunMainWithTelemetry(
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
		nil,
	)
	gomega.Expect(code).To(gomega.Equal(1), fmt.Sprintf("expected planner-without-artifact exit 1, got %d stderr=%s", code, stderr.String()))
	gomega.Expect(gotCfg.LlamaServer.Model).To(gomega.Equal("planner-override"), fmt.Sprintf("model override = %q, want planner-override", gotCfg.LlamaServer.Model))
	gomega.Expect(strings.TrimSpace(gotCfg.Workspace.SessionID)).NotTo(gomega.Equal(""), "delegated run should stamp workspace session id before planner call")
	gomega.Expect(gotPrompt).To(gomega.ContainSubstring("plan adapter delegation"), fmt.Sprintf("planner prompt should include original prompt, got:\n%s", gotPrompt))
	gomega.Expect(gotOpts.JSONEvents).To(gomega.BeTrue(), fmt.Sprintf("planner options should forward json/no-thinking flags, got %#v", gotOpts))
	gomega.Expect(gotOpts.DisableThinking).To(gomega.BeTrue(), fmt.Sprintf("planner options should forward json/no-thinking flags, got %#v", gotOpts))
	gomega.Expect(gotOpts.UseActiveModel).To(gomega.BeTrue(), fmt.Sprintf("model override should force active model routing, got %#v", gotOpts))
	gomega.Expect(gotOpts.TaskFamily).To(gomega.Equal(string(autonomy.TaskFamilyPlanThenExecutePlanner)), fmt.Sprintf("planner options should carry plan-execute planner contract, got %#v", gotOpts))
	gomega.Expect(gotOpts.PlannerOwnedVerification).To(gomega.BeTrue(), fmt.Sprintf("planner options should carry plan-execute planner contract, got %#v", gotOpts))
	gomega.Expect(gotOpts.MaxTokensOverride).To(gomega.Equal(3072), fmt.Sprintf("MaxTokensOverride = %d, want 3072", gotOpts.MaxTokensOverride))
	gomega.Expect(gotOpts.ScopeContract.ContractID).NotTo(gomega.Equal(""), fmt.Sprintf("planner options should carry resolved scope contract, got %#v", gotOpts.ScopeContract))
	gomega.Expect(gotOpts.ScopeContract.RequiredCoveragePaths).To(gomega.HaveLen(1), fmt.Sprintf("planner options should carry resolved scope contract, got %#v", gotOpts.ScopeContract))

	events := jsoneventstest.DecodeLines(ginkgo.GinkgoT(), stdout.String())
	if _, ok := jsoneventstest.FindByType(events, plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventPlanArtifactMissing)); !ok {
		ginkgo.Fail(fmt.Sprintf("delegated planner failure should preserve JSON event, got %s", stdout.String()))
	}
})
var _ = ginkgo.It("run plan execute main model override bypasses planner role alias", func() {
	root := ginkgo.GinkgoT().TempDir()
	startPlanExecuteTestQueue("qwen-35b")
	gomega.Expect(os.MkdirAll(filepath.Join(root, ".idiom"), 0o755)).To(gomega.Succeed())

	var aliasPlannerChat int
	aliasPlannerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": "local-planner"}}})
		case "/v1/chat/completions":
			aliasPlannerChat++
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]string{"content": "alias route"}}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer aliasPlannerServer.Close()

	var (
		configPlannerChat  int
		configPlannerModel string
	)
	configPlannerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer ginkgo.GinkgoRecover()

		switch r.URL.Path {
		case "/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": "deepseek-v4-flash"}}})
		case "/v1/chat/completions":
			configPlannerChat++

			var body struct {
				Model string `json:"model"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				ginkgo.Fail(fmt.Sprintf("decode chat body: %v", err))
			}
			configPlannerModel = body.Model

			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]string{"content": "no plan"}}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer configPlannerServer.Close()

	backendsJSON := fmt.Sprintf(`{"profiles":{"local":{"models":[
		{"alias":"local-planner","model":"local-planner","base_url":%q,"roles":["planner"]}
	]}}}`, aliasPlannerServer.URL)
	gomega.Expect(os.WriteFile(filepath.Join(root, ".idiom", "backends.json"), []byte(backendsJSON), 0o644)).To(gomega.Succeed())

	cfg := config.Default()
	cfg.LlamaServer.Kind = config.BackendDeepSeek
	cfg.LlamaServer.BaseURL = configPlannerServer.URL
	cfg.LlamaServer.Model = "deepseek-v4-pro"
	cfg.Chat.AutoStartServer = false
	cfg.Chat.AttachInstructionFiles = false
	cfg.Chat.SystemPromptFiles = nil
	cfg.Chat.DefaultProfiles = nil
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true
	cfg.Workspace.EnableShell = false
	cfg.RouteClassifier.Enabled = false
	cfg.MCP = nil

	configPath := filepath.Join(root, "config.json")
	data, err := json.Marshal(cfg)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal config: %v", err))
	gomega.Expect(os.WriteFile(configPath, data, 0o644)).To(gomega.Succeed())

	var stdout, stderr bytes.Buffer
	promptRun, _ := cmdruntime.NewHeadlessRunners(func(cfg config.Config) runtimesession.Engine {
		return app.New(cfg)
	})
	code := RunMainWithTelemetry(
		[]string{
			"-config", configPath,
			"-prompt", "plan this bounded atom",
			"-required-edit-path", "pkg.go",
			"-model", "deepseek-v4-flash",
		},
		&stdout,
		&stderr,
		promptRun,
		nil,
	)
	gomega.Expect(code).To(gomega.Equal(1), fmt.Sprintf("expected planner-without-artifact exit 1, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String()))
	gomega.Expect(configPlannerChat).NotTo(gomega.Equal(0), "expected config planner chat request")
	gomega.Expect(aliasPlannerChat).To(gomega.Equal(0), fmt.Sprintf("explicit model override should bypass planner role alias, got %d alias chats", aliasPlannerChat))
	gomega.Expect(configPlannerModel).To(gomega.Equal("deepseek-v4-flash"), fmt.Sprintf("config planner request model = %q, want deepseek-v4-flash", configPlannerModel))

})
var _ = ginkgo.It("run plan execute main uses shared existing file planner token budget", func() {
	configPath := writePlanExecuteConfig()

	var (
		stdout, stderr bytes.Buffer
		gotOpts        cmdruntime.RunOptions
	)

	code := RunMainWithTelemetry(
		[]string{"-config", configPath, "-prompt", "plan this", "-required-edit-path", "internal/surfaces/tui/app/rendering.go"},
		&stdout,
		&stderr,
		func(_ config.Config, _ string, opts cmdruntime.RunOptions, _ io.Writer) (string, error) {
			gotOpts = opts
			return "no plan", nil
		},
		nil,
	)
	gomega.Expect(code).To(gomega.Equal(1), fmt.Sprintf("expected planner-without-artifact exit 1, got %d stderr=%s", code, stderr.String()))
	gomega.Expect(gotOpts.MaxTokensOverride).To(gomega.Equal(16384), fmt.Sprintf("MaxTokensOverride = %d, want existing-file plan-execute budget 16384", gotOpts.MaxTokensOverride))

})
var _ = ginkgo.It("run plan execute main keeps large budget for new file planner", func() {
	configPath := writePlanExecuteConfig()

	var (
		stdout, stderr bytes.Buffer
		gotOpts        cmdruntime.RunOptions
	)

	code := RunMainWithTelemetry(
		[]string{"-config", configPath, "-prompt", "create new file with full file body", "-required-edit-path", "internal/surfaces/tui/app/new_feature.go"},
		&stdout,
		&stderr,
		func(_ config.Config, _ string, opts cmdruntime.RunOptions, _ io.Writer) (string, error) {
			gotOpts = opts
			return "no plan", nil
		},
		nil,
	)
	gomega.Expect(code).To(gomega.Equal(1), fmt.Sprintf("expected planner-without-artifact exit 1, got %d stderr=%s", code, stderr.String()))
	gomega.Expect(gotOpts.MaxTokensOverride).To(gomega.Equal(16384), fmt.Sprintf("MaxTokensOverride = %d, want large-body plan-execute budget 16384", gotOpts.MaxTokensOverride))

})
var _ = ginkgo.It("run plan execute main keeps large budget for created named path", func() {
	configPath := writePlanExecuteConfig()

	var (
		stdout, stderr bytes.Buffer
		gotOpts        cmdruntime.RunOptions
	)

	code := RunMainWithTelemetry(
		[]string{"-config", configPath, "-prompt", "Create internal/contracts/writerroute/decision.go", "-required-edit-path", "internal/contracts/writerroute/decision.go"},
		&stdout,
		&stderr,
		func(_ config.Config, _ string, opts cmdruntime.RunOptions, _ io.Writer) (string, error) {
			gotOpts = opts
			return "no plan", nil
		},
		nil,
	)
	gomega.Expect(code).To(gomega.Equal(1), fmt.Sprintf("expected planner-without-artifact exit 1, got %d stderr=%s", code, stderr.String()))
	gomega.Expect(gotOpts.MaxTokensOverride).To(gomega.Equal(16384), fmt.Sprintf("MaxTokensOverride = %d, want named create-path budget 16384", gotOpts.MaxTokensOverride))

})
var _ = ginkgo.It("run plan execute main keeps large budget for missing requested test path", func() {
	configPath := writePlanExecuteConfig()

	var (
		stdout, stderr bytes.Buffer
		gotOpts        cmdruntime.RunOptions
	)

	code := RunMainWithTelemetry(
		[]string{"-config", configPath, "-prompt", "add regression tests proving the parser rejects invalid routes", "-required-edit-path", "internal/parser/route_test.go"},
		&stdout,
		&stderr,
		func(_ config.Config, _ string, opts cmdruntime.RunOptions, _ io.Writer) (string, error) {
			gotOpts = opts
			return "no plan", nil
		},
		nil,
	)
	gomega.Expect(code).To(gomega.Equal(1), fmt.Sprintf("expected planner-without-artifact exit 1, got %d stderr=%s", code, stderr.String()))
	gomega.Expect(gotOpts.MaxTokensOverride).To(gomega.Equal(16384), fmt.Sprintf("MaxTokensOverride = %d, want missing requested-test budget 16384", gotOpts.MaxTokensOverride))

})
var _ = ginkgo.It("plan execute edit set obligation path classification", func() {
	for _, path := range []string{"tests/parser_test.py", "test/foo.py", "tests/panel_contract.gd"} {
		gomega.Expect(plancontract.PathLooksLikeTest(path)).To(gomega.BeTrue(), fmt.Sprintf("path should count as a test path: %s", path))

	}

	if reason := (plancontract.EditSetObligations{}).With(plancontract.EditSetObligationKindImplementationEdit, "test").RejectionReason([]string{"docs/feature.md", "pkg/feature_test.go"}); reason != plancontract.PlanExecuteFailureMissingRequestedImplementationEdit {
		ginkgo.Fail(fmt.Sprintf("docs plus tests should not satisfy implementation edit obligation, got %q", reason))
	}
})
var _ = ginkgo.It("run plan execute main json emits writer route decision", func() {
	configPath := writePlanExecuteConfig()

	var stdout, stderr bytes.Buffer

	code := RunMainWithTelemetry(
		[]string{"-config", configPath, "-prompt", "plan this change", "-json"},
		&stdout,
		&stderr,
		func(_ config.Config, _ string, _ cmdruntime.RunOptions, _ io.Writer) (string, error) {
			return "no plan", nil
		},
		nil,
	)
	gomega.Expect(code).To(gomega.Equal(1), fmt.Sprintf("expected planner-without-artifact exit 1, got %d stderr=%s", code, stderr.String()))

	events := jsoneventstest.DecodeLines(ginkgo.GinkgoT(), stdout.String())
	event, ok := jsoneventstest.FindByType(events, plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventWriterRouteDecision))
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("missing direct plan-execute writer route event in %s", stdout.String()))
	gomega.Expect(event["writer_mode"]).To(gomega.Equal("plan_submission"), fmt.Sprintf("unexpected direct plan-execute route event: %#v", event))
	gomega.Expect(event["lane"]).To(gomega.Equal("plan-execute"), fmt.Sprintf("unexpected direct plan-execute route event: %#v", event))
	gomega.Expect(event["verification_owner"]).To(gomega.Equal("planner_owned"), fmt.Sprintf("unexpected direct plan-execute route event: %#v", event))
	gomega.Expect(event["task_family"]).To(gomega.Equal(string(autonomy.TaskFamilyPlanThenExecutePlanner)), fmt.Sprintf("direct plan-execute route event should carry task_family, got %#v", event))
	gomega.Expect(event["cost_tier"]).To(gomega.Equal("local_first"), fmt.Sprintf("route event should carry local-first defaults: %#v", event))
	gomega.Expect(event["review_policy"]).To(gomega.Equal("local_first"), fmt.Sprintf("route event should carry local-first defaults: %#v", event))
	gomega.Expect(jsonEventStringSliceContains(event["reason_codes"], "route.multi_file_plan_execute")).To(gomega.BeTrue(), fmt.Sprintf("direct plan-execute route should explain multi-file plan-execute dispatch: %#v", event))

})
var _ = ginkgo.It("run plan execute main model override updates writer route decision", func() {
	configPath := writePlanExecuteConfig()
	root := filepath.Dir(configPath)
	gomega.Expect(os.MkdirAll(filepath.Join(root, ".idiom"), 0o755)).To(gomega.Succeed())

	backendsJSON := `{"profiles":{"local":{"models":[{"alias":"local-planner","model":"local-planner","roles":["planner"]},{"alias":"local-coder","model":"local-coder","roles":["executor"]},{"alias":"local-classifier","model":"local-classifier","roles":["classifier"]}]}}}`
	gomega.Expect(os.WriteFile(filepath.Join(root, ".idiom", "backends.json"), []byte(backendsJSON), 0o644)).To(gomega.Succeed())

	var stdout, stderr bytes.Buffer

	code := RunMainWithTelemetry(
		[]string{"-config", configPath, "-prompt", "plan this change", "-required-edit-path", "pkg.go", "-model", "deepseek-v4-flash", "-json"},
		&stdout,
		&stderr,
		func(_ config.Config, _ string, _ cmdruntime.RunOptions, _ io.Writer) (string, error) {
			return "no plan", nil
		},
		nil,
	)
	gomega.Expect(code).To(gomega.Equal(1), fmt.Sprintf("expected planner-without-artifact exit 1, got %d stderr=%s", code, stderr.String()))

	events := jsoneventstest.DecodeLines(ginkgo.GinkgoT(), stdout.String())
	event, ok := jsoneventstest.FindByType(events, plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventWriterRouteDecision))
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("missing writer route event in %s", stdout.String()))
	gomega.Expect(event["planner_model"]).To(gomega.Equal("deepseek-v4-flash"), fmt.Sprintf("explicit model override should own writer route event, got %#v", event))
	gomega.Expect(event["executor_model"]).To(gomega.Equal("deepseek-v4-flash"), fmt.Sprintf("explicit model override should own writer route event, got %#v", event))
	gomega.Expect(event["classifier_model"]).To(gomega.Equal("deepseek-v4-flash"), fmt.Sprintf("explicit model override should own writer route event, got %#v", event))
	gomega.Expect(event["effective_model"]).To(gomega.Equal("deepseek-v4-flash"), fmt.Sprintf("effective_model = %q, want deepseek-v4-flash; event=%#v", event["effective_model"], event))
	gomega.Expect(event["user_override"]).To(gomega.Equal(true), fmt.Sprintf("user_override = %v, want true; event=%#v", event["user_override"], event))
	gomega.Expect(fmt.Sprint(event)).NotTo(gomega.ContainSubstring("local-planner"), fmt.Sprintf("explicit model override should not report role aliases in route event, got %#v", event))
	gomega.Expect(fmt.Sprint(event)).NotTo(gomega.ContainSubstring("local-coder"), fmt.Sprintf("explicit model override should not report role aliases in route event, got %#v", event))
	gomega.Expect(fmt.Sprint(event)).NotTo(gomega.ContainSubstring("local-classifier"), fmt.Sprintf("explicit model override should not report role aliases in route event, got %#v", event))

	if aliases, ok := event["role_aliases"].(map[string]any); ok && len(aliases) > 0 {
		ginkgo.Fail(fmt.Sprintf("explicit model override should suppress role aliases in route event, got %#v", event))
	}
})
var _ = ginkgo.It("run plan execute main config model beats workspace role aliases in writer route decision", func() {
	configPath := writePlanExecuteConfig()
	root := filepath.Dir(configPath)
	configBody := fmt.Sprintf(`{"workspace":{"root":%q,"cwd":%q},"llama_server":{"kind":"deepseek","base_url":"https://api.deepseek.com","model":"deepseek-v4-flash"}}`, root, root)
	gomega.Expect(os.WriteFile(configPath, []byte(configBody), 0o644)).To(gomega.Succeed())
	gomega.Expect(os.MkdirAll(filepath.Join(root, ".idiom"), 0o755)).To(gomega.Succeed())

	backendsJSON := `{"profiles":{"local":{"models":[{"alias":"local-planner","model":"local-planner","roles":["planner"]},{"alias":"local-coder","model":"local-coder","roles":["executor"]},{"alias":"local-classifier","model":"local-classifier","roles":["classifier"]}]}}}`
	gomega.Expect(os.WriteFile(filepath.Join(root, ".idiom", "backends.json"), []byte(backendsJSON), 0o644)).To(gomega.Succeed())

	var (
		stdout, stderr bytes.Buffer
		gotCfg         config.Config
		gotOpts        cmdruntime.RunOptions
	)
	code := RunMainWithTelemetry(
		[]string{"-config", configPath, "-prompt", "plan this change", "-required-edit-path", "pkg.go", "-json"},
		&stdout,
		&stderr,
		func(cfg config.Config, _ string, opts cmdruntime.RunOptions, _ io.Writer) (string, error) {
			gotCfg = cfg
			gotOpts = opts
			return "no plan", nil
		},
		nil,
	)
	gomega.Expect(code).To(gomega.Equal(1), fmt.Sprintf("expected planner-without-artifact exit 1, got %d stderr=%s", code, stderr.String()))

	events := jsoneventstest.DecodeLines(ginkgo.GinkgoT(), stdout.String())
	event, ok := jsoneventstest.FindByType(events, plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventWriterRouteDecision))
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("missing writer route event in %s", stdout.String()))

	for _, field := range []string{"planner_model", "executor_model", "classifier_model", "effective_model"} {
		gomega.Expect(event[field]).To(gomega.Equal("deepseek-v4-flash"), fmt.Sprintf("%s = %q, want deepseek-v4-flash; event=%#v", field, event[field], event))

	}
	gomega.Expect(event["user_override"]).NotTo(gomega.Equal(true), fmt.Sprintf("config model alone should not be reported as a user override; event=%#v", event))
	gomega.Expect(gotCfg.LlamaServer.Model).To(gomega.Equal("deepseek-v4-flash"), fmt.Sprintf("prompt runner cfg model = %q, want deepseek-v4-flash", gotCfg.LlamaServer.Model))
	gomega.Expect(gotOpts.UseActiveModel).To(gomega.BeTrue(), fmt.Sprintf("config model should force active model routing so role aliases cannot override execution, got %#v", gotOpts))

})
var _ = ginkgo.It("plan execute planner addendum avoids hard read caps", func() {
	addendum := planexecute.PlanExecutePlannerSystemAddendumWithObligations(nil, nil, nil)
	for _, forbidden := range []string{
		"At most one bounded source-read",
		"Never re-read the same file",
	} {
		gomega.Expect(addendum).NotTo(gomega.ContainSubstring(forbidden), fmt.Sprintf("planner addendum still contains hard read-cap wording %q:\n%s", forbidden, addendum))

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
		gomega.Expect(addendum).To(gomega.ContainSubstring(want), fmt.Sprintf("planner addendum missing readiness-oriented wording %q:\n%s", want, addendum))

	}
	gomega.Expect(addendum).NotTo(gomega.ContainSubstring("path/from/required_edit_paths"), fmt.Sprintf("planner addendum must not contain copyable placeholder paths:\n%s", addendum))

})
var _ = ginkgo.It("plan execute planner addendum includes required path checklist", func() {
	addendum := planexecute.PlanExecutePlannerSystemAddendumWithObligations([]string{"a.cpp", "b.cpp", "a.cpp"}, nil, nil)

	for _, want := range []string{
		"Required path coverage for this run: 2 required path(s).",
		"- a.cpp",
		"- b.cpp",
		"If at least one required path needs a change",
		"top-level `source_satisfied_paths` only for already-final paths",
		"If every required path already contains the requested final state, do not call `submit_plan`; answer `Decision: source-satisfied`",
		"Do not submit a partial plan",
	} {
		gomega.Expect(addendum).To(gomega.ContainSubstring(want), fmt.Sprintf("planner addendum missing %q:\n%s", want, addendum))

	}
})
var _ = ginkgo.It("plan execute planner addendum includes allowed path checklist", func() {
	addendum := planexecute.PlanExecutePlannerSystemAddendumWithObligations(nil, []string{"internal/surfaces/tui/app/rendering.go", "internal/surfaces/tui/app/rendering.go"}, nil)

	for _, want := range []string{
		"Allowed `submit_plan` edit scope for this run: 1 candidate path(s).",
		"- internal/surfaces/tui/app/rendering.go",
		"allowlist, not coverage obligations",
	} {
		gomega.Expect(addendum).To(gomega.ContainSubstring(want), fmt.Sprintf("planner addendum missing %q:\n%s", want, addendum))

	}
})
var _ = ginkgo.It("run plan execute main auto exact plan skips planner model", func() {
	configPath := writePlanExecuteConfig()
	root := filepath.Dir(configPath)
	gomega.Expect(os.WriteFile(filepath.Join(root, "a.txt"), []byte("old a\n"), 0o644)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(filepath.Join(root, "b.txt"), []byte("old b\n"), 0o644)).To(gomega.Succeed())

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

	code := RunMainWithTelemetry(
		[]string{"-config", configPath, "-prompt", prompt, "-json", "-skip-review-repair"},
		&stdout,
		&stderr,
		func(_ config.Config, _ string, _ cmdruntime.RunOptions, _ io.Writer) (string, error) {
			modelCalled = true
			return "", fmt.Errorf("planner model should not be called for complete exact-anchor packets")
		},
		nil,
	)
	gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("auto exact plan should execute cleanly, got %d stderr=%s stdout=%s", code, stderr.String(), stdout.String()))
	gomega.Expect(modelCalled).To(gomega.BeFalse(), "planner model was called despite complete exact-anchor packet")

	if got := readFileString(filepath.Join(root, "a.txt")); got != "new a\n" {
		ginkgo.Fail(fmt.Sprintf("a.txt = %q", got))
	}

	if got := readFileString(filepath.Join(root, "b.txt")); got != "new b\n" {
		ginkgo.Fail(fmt.Sprintf("b.txt = %q", got))
	}
	gomega.Expect(stdout.String()).To(gomega.ContainSubstring("auto_exact_plan_submitted"), fmt.Sprintf("auto exact plan event missing from stdout=%s", stdout.String()))

})
var _ = ginkgo.It("run plan execute main auto exact plan source satisfied skips planner model", func() {
	configPath := writePlanExecuteConfig()
	root := filepath.Dir(configPath)
	gomega.Expect(os.WriteFile(filepath.Join(root, "a.txt"), []byte("new a\n"), 0o644)).To(gomega.Succeed())

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

	code := RunMainWithTelemetry(
		[]string{"-config", configPath, "-prompt", prompt, "-json", "-skip-review-repair"},
		&stdout,
		&stderr,
		func(_ config.Config, _ string, _ cmdruntime.RunOptions, _ io.Writer) (string, error) {
			modelCalled = true
			return "", fmt.Errorf("planner model should not be called for source-satisfied exact-anchor packets")
		},
		nil,
	)
	gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("source-satisfied exact plan should pass, got %d stderr=%s stdout=%s", code, stderr.String(), stdout.String()))
	gomega.Expect(modelCalled).To(gomega.BeFalse(), "planner model was called despite source-satisfied exact-anchor packet")

	if got := readFileString(filepath.Join(root, "a.txt")); got != "new a\n" {
		ginkgo.Fail(fmt.Sprintf("a.txt = %q", got))
	}

	for _, want := range []string{"writer_route_decision", "auto_exact_plan_source_satisfied", "source_satisfied"} {
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring(want), fmt.Sprintf("expected stdout to contain %q, got %s", want, stdout.String()))

	}
})
var _ = ginkgo.It("run plan execute main auto exact plan infers unlabeled multi file and new file", func() {
	configPath := writePlanExecuteConfig()
	root := filepath.Dir(configPath)
	gomega.Expect(os.WriteFile(filepath.Join(root, "a.txt"), []byte("alpha\nomega\n"), 0o644)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(filepath.Join(root, "b.txt"), []byte("ticks row\nfooter\n"), 0o644)).To(gomega.Succeed())

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

	code := RunMainWithTelemetry(
		[]string{"-config", configPath, "-prompt", prompt, "-json", "-skip-review-repair", "-authorized-new-file", "c.json"},
		&stdout,
		&stderr,
		func(_ config.Config, _ string, _ cmdruntime.RunOptions, _ io.Writer) (string, error) {
			modelCalled = true
			return "", fmt.Errorf("planner model should not be called for inferable exact-anchor packets")
		},
		nil,
	)
	gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("auto exact plan should infer multi-file paths and new-file body, got %d stderr=%s stdout=%s", code, stderr.String(), stdout.String()))
	gomega.Expect(modelCalled).To(gomega.BeFalse(), "planner model was called despite inferable exact-anchor packet")

	if got := readFileString(filepath.Join(root, "a.txt")); got != "alpha\nseed\nomega\n" {
		ginkgo.Fail(fmt.Sprintf("a.txt = %q", got))
	}

	if got := readFileString(filepath.Join(root, "b.txt")); got != "ticks row\nlast result\nfooter\n" {
		ginkgo.Fail(fmt.Sprintf("b.txt = %q", got))
	}

	if got := readFileString(filepath.Join(root, "c.json")); got != "{\"ok\": true}" {
		ginkgo.Fail(fmt.Sprintf("c.json = %q", got))
	}
	gomega.Expect(stdout.String()).To(gomega.ContainSubstring("auto_exact_plan_submitted"), fmt.Sprintf("auto exact plan event missing from stdout=%s", stdout.String()))

})
var _ = ginkgo.It("run plan execute main required edit path flag enables auto exact plan", func() {
	configPath := writePlanExecuteConfig()
	root := filepath.Dir(configPath)
	gomega.Expect(os.WriteFile(filepath.Join(root, "a.txt"), []byte("old a\n"), 0o644)).To(gomega.Succeed())

	prompt := "## Required Edits\n\n" +
		"old_text:\n\n```text\nold a\n```\n\n" +
		"new_text:\n\n```text\nnew a\n```\n"

	var stdout, stderr bytes.Buffer

	modelCalled := false

	code := RunMainWithTelemetry(
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
		nil,
	)
	gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("auto exact plan should execute cleanly with explicit required path, got %d stderr=%s stdout=%s", code, stderr.String(), stdout.String()))
	gomega.Expect(modelCalled).To(gomega.BeFalse(), "planner model was called despite explicit required path and complete exact-anchor packet")

	if got := readFileString(filepath.Join(root, "a.txt")); got != "new a\n" {
		ginkgo.Fail(fmt.Sprintf("a.txt = %q", got))
	}

	out := stdout.String()
	gomega.Expect(out).To(gomega.ContainSubstring("required_edit_paths_resolved"), fmt.Sprintf("expected required-path resolution and auto-plan events, got stdout=%s", out))
	gomega.Expect(out).To(gomega.ContainSubstring("auto_exact_plan_submitted"), fmt.Sprintf("expected required-path resolution and auto-plan events, got stdout=%s", out))
	gomega.Expect(out).NotTo(gomega.ContainSubstring("no required_edit_paths"), fmt.Sprintf("explicit required path should avoid no-required-path skip, stdout=%s", out))

})
var _ = ginkgo.It("run plan execute main treats source satisfied planner answer as clean noop", func() {
	configPath := writePlanExecuteConfig()
	prompt := "## Files To Modify\n\n| File | Change |\n|------|--------|\n| `a.go` | update existing behavior |\n| `b.go` | update existing test |\n"

	var stdout, stderr bytes.Buffer

	code := RunMainWithTelemetry(
		[]string{"-config", configPath, "-prompt", prompt},
		&stdout,
		&stderr,
		func(_ config.Config, _ string, _ cmdruntime.RunOptions, _ io.Writer) (string, error) {
			return sourceSatisfiedPlannerAnswerForCLITest(
				"- a.go: already implemented; no changes needed.",
				"- b.go: already implemented; no changes needed.",
			), nil
		},
		nil,
	)
	gomega.Expect(code).To(gomega.Equal(1), fmt.Sprintf("source-satisfied planner answer without typed current-state evidence should exit 1, got %d stderr=%s stdout=%s", code, stderr.String(), stdout.String()))
	gomega.Expect(stderr.String()).NotTo(gomega.ContainSubstring("plan artifact"), fmt.Sprintf("source-satisfied closeout should not report missing plan artifact, stderr=%s", stderr.String()))
	gomega.Expect(stdout.String()).To(gomega.ContainSubstring("source-satisfied: executor skipped"), fmt.Sprintf("source-satisfied closeout should be visible, stdout=%s", stdout.String()))

})
var _ = ginkgo.It("run plan execute main treats unscoped source satisfied planner answer as clean noop", func() {
	configPath := writePlanExecuteConfig()
	prompt := "Reject temp symlinks and add regression tests proving no partial mutation."

	var stdout, stderr bytes.Buffer

	code := RunMainWithTelemetry(
		[]string{"-config", configPath, "-prompt", prompt},
		&stdout,
		&stderr,
		func(_ config.Config, _ string, _ cmdruntime.RunOptions, _ io.Writer) (string, error) {
			return sourceSatisfiedPlannerAnswerForCLITest(
				"- internal/workflows/productworkflow/migration_apply.go: no changes needed.",
				"- internal/workflows/productworkflow/migration_apply_test.go: no changes needed.",
			), nil
		},
		nil,
	)
	gomega.Expect(code).To(gomega.Equal(1), fmt.Sprintf("unscoped source-satisfied planner answer without typed current-state evidence should exit 1, got %d stderr=%s stdout=%s", code, stderr.String(), stdout.String()))
	gomega.Expect(stderr.String()).NotTo(gomega.ContainSubstring("plan artifact"), fmt.Sprintf("source-satisfied closeout should not report missing plan artifact, stderr=%s", stderr.String()))
	gomega.Expect(stdout.String()).To(gomega.ContainSubstring("source-satisfied: executor skipped"), fmt.Sprintf("source-satisfied closeout should be visible, stdout=%s", stdout.String()))

})
var _ = ginkgo.It("run plan execute main does not infer implementation obligation from prompt", func() {
	configPath := writePlanExecuteConfig()
	prompt := "Implement the feature and add tests."

	var stdout, stderr bytes.Buffer

	code := RunMainWithTelemetry(
		[]string{"-config", configPath, "-prompt", prompt, "-json"},
		&stdout,
		&stderr,
		func(_ config.Config, _ string, _ cmdruntime.RunOptions, _ io.Writer) (string, error) {
			return sourceSatisfiedPlannerAnswerForCLITest("- pkg/feature_test.go: feature tests are already present."), nil
		},
		nil,
	)
	gomega.Expect(code).To(gomega.Equal(1), fmt.Sprintf("prompt prose should not create a hidden implementation obligation, but source-satisfied still needs typed evidence; got %d stderr=%s stdout=%s", code, stderr.String(), stdout.String()))

	events := jsoneventstest.DecodeLines(ginkgo.GinkgoT(), stdout.String())
	if _, ok := jsoneventstest.FindByType(events, plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventSourceSatisfiedRejected)); ok {
		ginkgo.Fail(fmt.Sprintf("prompt prose must not produce source_satisfied_rejected, got %s", stdout.String()))
	}
})
var _ = ginkgo.It("run plan execute main still fails missing artifact without source satisfied evidence", func() {
	configPath := writePlanExecuteConfig()
	prompt := "## Files To Modify\n\n| File | Change |\n|------|--------|\n| `a.go` | update existing behavior |\n| `b.go` | update existing test |\n"

	var stdout, stderr bytes.Buffer

	code := RunMainWithTelemetry(
		[]string{"-config", configPath, "-prompt", prompt},
		&stdout,
		&stderr,
		func(_ config.Config, _ string, _ cmdruntime.RunOptions, _ io.Writer) (string, error) {
			return sourceSatisfiedPlannerAnswerForCLITest("- a.go: already implemented; no changes needed."), nil
		},
		nil,
	)
	gomega.Expect(code).To(gomega.Equal(1), fmt.Sprintf("missing source-satisfied path evidence should still fail missing artifact, got %d stderr=%s stdout=%s", code, stderr.String(), stdout.String()))
	gomega.Expect(stderr.String()).To(gomega.ContainSubstring("planner phase did not produce a plan artifact"), fmt.Sprintf("expected normal missing-artifact failure, stderr=%s", stderr.String()))

})
var _ = ginkgo.It("run plan execute main fails missing artifact despite explicit full body new file brief", func() {
	configPath := writePlanExecuteConfig()
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

	code := RunMainWithTelemetry(
		[]string{"-config", configPath, "-prompt", prompt, "-json", "-skip-review-repair"},
		&stdout,
		&stderr,
		func(_ config.Config, _ string, _ cmdruntime.RunOptions, _ io.Writer) (string, error) {
			return "I can create docs/generated-note.txt, but I did not call submit_plan.", nil
		},
		nil,
	)
	gomega.Expect(code).To(gomega.Equal(1), fmt.Sprintf("missing planner artifact must fail even with inline full body, got %d stderr=%s stdout=%s", code, stderr.String(), stdout.String()))
	gomega.Expect(stderr.String()).To(gomega.ContainSubstring("planner phase did not produce a plan artifact"), fmt.Sprintf("expected missing-artifact failure, stderr=%s", stderr.String()))

	if _, err := os.Stat(filepath.Join(root, "docs", "generated-note.txt")); !os.IsNotExist(err) {
		ginkgo.Fail(fmt.Sprintf("host fallback must not create a file from prompt body, stat err=%v", err))
	}
	gomega.Expect(stdout.String()).NotTo(gomega.ContainSubstring(plancontract.PlanExecuteEventPrefix+"auto_submit_full_body_plan"), fmt.Sprintf("host fallback telemetry must not be emitted, stdout=%s", stdout.String()))
	gomega.Expect(stdout.String()).To(gomega.ContainSubstring(plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventPlanArtifactMissing)), fmt.Sprintf("expected missing-artifact telemetry event, stdout=%s", stdout.String()))

})
var _ = ginkgo.It("run plan execute main does not auto submit real question blocker", func() {
	configPath := writePlanExecuteConfig()
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

	code := RunMainWithTelemetry(
		[]string{"-config", configPath, "-prompt", prompt, "-skip-review-repair"},
		&stdout,
		&stderr,
		func(_ config.Config, _ string, _ cmdruntime.RunOptions, _ io.Writer) (string, error) {
			return "<<QUESTION>>: docs/generated-note.txt: current API evidence contradicts the requested body; do not create the file until the brief is rebased.", nil
		},
		nil,
	)
	gomega.Expect(code).To(gomega.Equal(1), fmt.Sprintf("real question blockers must not be auto-submitted, got %d stderr=%s stdout=%s", code, stderr.String(), stdout.String()))

	if _, err := os.Stat(filepath.Join(root, "docs", "generated-note.txt")); !os.IsNotExist(err) {
		ginkgo.Fail(fmt.Sprintf("auto-submit created a file despite a real blocker, stat err=%v", err))
	}
	gomega.Expect(stderr.String()).To(gomega.ContainSubstring("planner phase reported a blocker before submitting a plan"), fmt.Sprintf("explicit blocker should be surfaced as planner_blocked, stderr=%s", stderr.String()))
	gomega.Expect(stderr.String()).NotTo(gomega.ContainSubstring("planner phase did not produce a plan artifact"), fmt.Sprintf("explicit blocker should not be masked as missing artifact, stderr=%s", stderr.String()))

	records, err := filepath.Glob(filepath.Join(root, ".idiom", "recovery", "plan-execute-*", "failure.json"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("glob recovery records: %v", err))
	gomega.Expect(records).To(gomega.HaveLen(1), fmt.Sprintf("expected one automatic recovery record, got %d: %v", len(records), records))

	if body := readFileString(records[0]); !strings.Contains(body, `"class": "planner_blocked"`) ||
		!strings.Contains(body, `"next_valid_action": "answer_blocker"`) {
		ginkgo.Fail(fmt.Sprintf("unexpected recovery record:\n%s", body))
	}
})
var _ = ginkgo.It("run plan execute main does not treat question blocker mentioning source satisfied as clean noop", func() {
	configPath := writePlanExecuteConfig()
	prompt := "## Files To Modify\n\n| File | Change |\n|------|--------|\n| `a.go` | update existing behavior |\n"

	var stdout, stderr bytes.Buffer

	code := RunMainWithTelemetry(
		[]string{"-config", configPath, "-prompt", prompt},
		&stdout,
		&stderr,
		func(_ config.Config, _ string, _ cmdruntime.RunOptions, _ io.Writer) (string, error) {
			return fmt.Sprintf("<<QUESTION>>: Completion gate rejected before submit_plan.\n- a.go: no successful edit was recorded; provide submit_plan, %s evidence, or a path-specific blocker.", plancontract.SourceSatisfiedDecision), nil
		},
		nil,
	)
	gomega.Expect(code).To(gomega.Equal(1), fmt.Sprintf("blocker that merely mentions source-satisfied must fail typed blocker, got %d stderr=%s stdout=%s", code, stderr.String(), stdout.String()))
	gomega.Expect(stderr.String()).To(gomega.ContainSubstring("planner phase reported a blocker before submitting a plan"), fmt.Sprintf("expected typed planner blocker for blocker answer, stderr=%s", stderr.String()))
	gomega.Expect(stderr.String()).NotTo(gomega.ContainSubstring("planner phase did not produce a plan artifact"), fmt.Sprintf("blocker should not be masked as missing artifact, stderr=%s", stderr.String()))
	gomega.Expect(stdout.String()).NotTo(gomega.ContainSubstring("source-satisfied: executor skipped"), fmt.Sprintf("blocker must not be reported as source-satisfied, stdout=%s", stdout.String()))

})
var _ = ginkgo.It("run plan execute main inherits pack before executor", func() {
	root := ginkgo.GinkgoT().TempDir()
	gomega.Expect(os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/planexec\n\ngo 1.22\n"), 0o644)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(filepath.Join(root, "a.go"), []byte("package pkg\n\nfunc A() string { return \"alpha\" }\n"), 0o644)).To(gomega.Succeed())

	build, err := comprehension.Build(ginkgo.GinkgoT().Context(), comprehension.BuildOptions{Root: root, Language: "go"})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("Build: %v", err))

	key := comprehension.ViewKey{IndexID: build.IndexID, Task: "inherit pack", Mode: "plan"}
	packID := key.ID()
	gomega.Expect(comprehension.SaveView(root, key, comprehension.PackResult{
		PackID:         packID,
		IndexID:        build.IndexID,
		Root:           root,
		Task:           "inherit pack",
		Mode:           "plan",
		Fresh:          true,
		CandidateFiles: []comprehension.PackFile{{Path: "a.go", Role: "editable"}},
	})).To(gomega.Succeed())

	configPath := filepath.Join(root, "config.json")
	body := fmt.Sprintf(`{"workspace":{"root":%q,"cwd":%q,"enable_writes":true}}`, root, root)
	gomega.Expect(os.WriteFile(configPath, []byte(body), 0o644)).To(gomega.Succeed())

	prompt := "## Context Pack\n\n- Pack: `" + packID + "`\n\nChange a.go from alpha to beta."

	var stdout, stderr bytes.Buffer

	code := RunMainWithTelemetry(
		[]string{"-config", configPath, "-json", "-skip-review-repair", "-prompt", prompt},
		&stdout,
		&stderr,
		func(cfg config.Config, _ string, _ cmdruntime.RunOptions, _ io.Writer) (string, error) {
			writePlanExecuteTestArtifact(cfg.Workspace.Root, cfg.Workspace.SessionID, planartifact.PlanArtifact{
				SchemaVersion: planartifact.SchemaVersion,
				Created:       time.Now().UTC().Format(time.RFC3339Nano),
				SessionID:     cfg.Workspace.SessionID,
				Rationale:     "change marker",
				Edits: []planartifact.PlanEdit{
					{Path: "a.go", OldText: "alpha", NewText: "beta", Purpose: "change marker"},
				},
			})

			return "submitted", nil
		},
		nil,
	)
	gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("RunMain exit=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String()))
	gomega.Expect(stdout.String()).To(gomega.ContainSubstring(plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventPlanArtifactPackInherited)), fmt.Sprintf("expected inherited pack event, got:\n%s", stdout.String()))
	gomega.Expect(stdout.String()).To(gomega.ContainSubstring(plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventExecutorInheritsPack)), fmt.Sprintf("expected executor to inherit pack after prompt inheritance, got:\n%s", stdout.String()))

})
var _ = ginkgo.It("run plan execute main does not infer test edit obligation before writes", func() {
	configPath := writePlanExecuteConfig()
	root := filepath.Dir(configPath)
	gomega.Expect(os.WriteFile(filepath.Join(root, "pkg.go"), []byte("package demo\n\nfunc Hardened() bool { return false }\n"), 0o644)).To(gomega.Succeed())

	var stdout, stderr bytes.Buffer

	code := RunMainWithTelemetry(
		[]string{
			"-config", configPath,
			"-prompt", "Implement the hardening and add regression tests proving the edge case is rejected.",
			"-json",
			"-skip-review-repair",
		},
		&stdout,
		&stderr,
		func(cfg config.Config, _ string, opts cmdruntime.RunOptions, _ io.Writer) (string, error) {
			gomega.Expect(opts.TaskFamily).To(gomega.Equal(string(autonomy.TaskFamilyPlanThenExecutePlanner)), fmt.Sprintf("unexpected task family %q", opts.TaskFamily))

			writePlanExecuteTestArtifact(cfg.Workspace.Root, cfg.Workspace.SessionID, planartifact.PlanArtifact{
				SchemaVersion: planartifact.SchemaVersion,
				Created:       time.Now().UTC().Format(time.RFC3339Nano),
				SessionID:     cfg.Workspace.SessionID,
				Rationale:     "production-only plan",
				Edits: []planartifact.PlanEdit{
					{Path: "pkg.go", OldText: "return false", NewText: "return true", Purpose: "harden behavior"},
				},
			})

			return "submitted production-only plan", nil
		},
		nil,
	)
	gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("prompt prose should not create a hidden test obligation, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String()))

	if got := readFileString(filepath.Join(root, "pkg.go")); !strings.Contains(got, "return true") {
		ginkgo.Fail(fmt.Sprintf("production edit should apply when no typed test obligation exists, got:\n%s", got))
	}
})
var _ = ginkgo.It("run plan execute main does not infer implementation obligation before writes", func() {
	configPath := writePlanExecuteConfig()
	root := filepath.Dir(configPath)
	gomega.Expect(os.WriteFile(filepath.Join(root, "feature.go"), []byte("package demo\n\nfunc Enabled() bool { return false }\n"), 0o644)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(filepath.Join(root, "feature_test.go"), []byte("package demo\n\nfunc TestEnabled(t *testing.T) {}\n"), 0o644)).To(gomega.Succeed())

	var stdout, stderr bytes.Buffer

	code := RunMainWithTelemetry(
		[]string{
			"-config", configPath,
			"-prompt", "Implement the feature. Start by adding failing tests, then implement.",
			"-json",
			"-skip-review-repair",
		},
		&stdout,
		&stderr,
		func(cfg config.Config, _ string, opts cmdruntime.RunOptions, _ io.Writer) (string, error) {
			gomega.Expect(opts.TaskFamily).To(gomega.Equal(string(autonomy.TaskFamilyPlanThenExecutePlanner)), fmt.Sprintf("unexpected task family %q", opts.TaskFamily))

			writePlanExecuteTestArtifact(cfg.Workspace.Root, cfg.Workspace.SessionID, planartifact.PlanArtifact{
				SchemaVersion: planartifact.SchemaVersion,
				Created:       time.Now().UTC().Format(time.RFC3339Nano),
				SessionID:     cfg.Workspace.SessionID,
				Rationale:     "test-only plan",
				Edits: []planartifact.PlanEdit{
					{Path: "feature_test.go", OldText: "func TestEnabled(t *testing.T) {}\n", NewText: "func TestEnabled(t *testing.T) {\n\tif !Enabled() {\n\t\tt.Fatal(\"disabled\")\n\t}\n}\n", Purpose: "add feature test"},
				},
			})

			return "submitted test-only plan", nil
		},
		nil,
	)
	gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("prompt prose should not create a hidden implementation obligation, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String()))

	if got := readFileString(filepath.Join(root, "feature_test.go")); !strings.Contains(got, "t.Fatal") {
		ginkgo.Fail(fmt.Sprintf("test-only edit should apply when no typed implementation obligation exists, got:\n%s", got))
	}
})

func writePlanExecuteConfig() string {

	root := ginkgo.GinkgoT().TempDir()
	startPlanExecuteTestQueue("qwen-35b")

	path := filepath.Join(root, "config.json")
	body := fmt.Sprintf(`{"workspace":{"root":%q,"cwd":%q}}`, root, root)
	gomega.Expect(os.WriteFile(path, []byte(body), 0o644)).To(gomega.Succeed())

	return path
}

func startPlanExecuteTestQueue(pool string) {

	specID := sha256.Sum256([]byte(ginkgo.GinkgoT().Name()))
	socket := filepath.Join("/tmp", fmt.Sprintf("idiom-%x.sock", specID[:8]))
	_ = os.Remove(socket)
	srv := runner.NewServer(runner.Config{Pools: map[string]runner.PoolConfig{pool: {MaxInFlight: 1}}}, socket)
	gomega.Expect(srv.Start()).To(gomega.Succeed())
	ginkgo.DeferCleanup(func() {
		srv.Stop()
		_ = os.Remove(socket)
	})
	ginkgo.GinkgoT().
		Setenv("IDIOM_QUEUE_POOL", pool)
	ginkgo.GinkgoT().
		Setenv("IDIOM_QUEUE_SOCKET", socket)
}

func scriptedIntentLoopTerminal(sessionID string, cycle int) plancontract.PlanExecuteTerminalCompletionRecord {
	command := "go test ./internal/intentloop-e2e/..."
	status := "passed"
	errText := ""
	exitCode := 0
	claims := []plancontract.AcceptanceEvidenceClaim{{
		ID:    "coverage-threshold",
		Value: "86%",
		Met:   true,
	}}

	switch cycle {
	case 1:
		status = "failed"
		errText = "internal/intentloop-e2e/target_test.go: missing edge case"
		exitCode = 1
		claims = nil
	case 2:
		claims = []plancontract.AcceptanceEvidenceClaim{{
			ID:    "coverage-threshold",
			Value: "72%",
			Met:   false,
		}}
	}

	return plancontract.NormalizePlanExecuteTerminalCompletionRecord(plancontract.PlanExecuteTerminalCompletionRecord{
		SchemaVersion:              plancontract.PlanExecuteTerminalCompletionSchemaVersion,
		Type:                       plancontract.PlanExecuteTerminalCompletionRecordType,
		TS:                         time.Date(2026, 6, 18, 13, cycle, 0, 0, time.UTC).Format(time.RFC3339Nano),
		SessionID:                  sessionID,
		Phase:                      plancontract.PlanExecuteProgressPhaseComplete,
		Status:                     plancontract.PlanExecuteTerminalStatusCompleted,
		ReviewStatus:               plancontract.PlanExecuteReviewStatusSkipped,
		TerminalClass:              plancontract.PlanExecuteTerminalClassSuccess,
		Outcome:                    plancontract.PlanExecuteTerminalOutcomeCompleted,
		ExitCode:                   exitCode,
		ReviewRepairSkipped:        true,
		ChangedPaths:               []string{"internal/intentloop_e2e_target.go"},
		RequiredEditPaths:          []string{"internal/intentloop_e2e_target.go"},
		SourceSanity:               []plancontract.PhaseEvidenceCheck{{Kind: "source_sanity", Path: "internal/intentloop_e2e_target.go", Status: plancontract.PhaseEvidenceStatus(status), Error: errText}},
		VerificationCommands:       []plancontract.VerificationCommand{{Command: command, Source: "scripted_live_e2e"}},
		Verification:               []plancontract.PhaseEvidenceCheck{{Kind: "verification", Command: command, Status: plancontract.PhaseEvidenceStatus(status), Error: errText}},
		AcceptanceEvidenceClaims:   claims,
		AcceptanceEvidenceIDs:      []string{},
		RequiredAcceptanceEvidence: []plancontract.AcceptanceEvidenceRequired{{ID: "coverage-threshold", Required: true, Threshold: ">=80%"}},
	})
}

func readFileString(path string) string {

	data, err := os.ReadFile(path)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read %s: %v", path, err))

	return string(data)
}

func initGitRepoForStaleness() string {

	root := ginkgo.GinkgoT().TempDir()

	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "test"},
	} {
		cmd := exec.Command("git", args...)

		cmd.Dir = root

		if out, err := cmd.CombinedOutput(); err != nil {
			ginkgo.Fail(fmt.Sprintf("git %v: %v: %s", args, err, out))
		}
	}

	return root
}

func gitCommit(root, path, content, message string) {

	full := filepath.Join(root, path)
	gomega.Expect(os.MkdirAll(filepath.Dir(full), 0o755)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(full, []byte(content), 0o644)).To(gomega.Succeed())

	for _, args := range [][]string{
		{"add", path},
		{"commit", "-q", "-m", message},
	} {
		cmd := exec.Command("git", args...)

		cmd.Dir = root

		if out, err := cmd.CombinedOutput(); err != nil {
			ginkgo.Fail(fmt.Sprintf("git %v: %v: %s", args, err, out))
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

func sourceSatisfiedPlannerAnswerForCLITest(lines ...string) string {
	return strings.Join(append([]string{plancontract.SourceSatisfiedDecision}, lines...), "\n")
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

var _ = ginkgo.It("run executor phase emits executor inherits pack event", func(

// When the planner stamps a pack_id and the cache row exists, the
// executor phase emits an executor_inherits_pack JSON event so
// downstream tooling can attach the planner's ranked context.
) {

	root := ginkgo.GinkgoT().TempDir()
	gomega.Expect(os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/planexec\n\ngo 1.22\n"), 0o644)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(filepath.Join(root, "a.go"), []byte("package pkg\n\nfunc A() string { return \"alpha\" }\n"), 0o644)).To(gomega.Succeed())

	build, err := comprehension.Build(ginkgo.GinkgoT().Context(), comprehension.BuildOptions{Root: root, Language: "go"})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("Build: %v", err))

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
	gomega.Expect(comprehension.SaveView(root, key, cached)).To(gomega.Succeed())

	plan := planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		PackID:        key.ID(),
		Edits: []planartifact.PlanEdit{
			{Path: "a.go", OldText: "alpha", NewText: "ALPHA"},
		},
	}
	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}

	var stdout bytes.Buffer

	if _, err := runExecutorPhaseForTest(cfg, plan, planexecute.ExecutorPhaseOptions{RequiredEditPaths: nil, AllowedEditPaths: nil, VerificationCommands: nil, JSONEvents: true, Stdout: &stdout}); err != nil {
		ginkgo.Fail(fmt.Sprintf("executor phase failed: %v", err))
	}
	gomega.Expect(stdout.String()).To(gomega.ContainSubstring(plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventExecutorInheritsPack)), fmt.Sprintf("expected executor_inherits_pack JSON event in stdout, got: %s", stdout.String()))

	events := jsoneventstest.DecodeLines(ginkgo.GinkgoT(), stdout.String())
	handoff, ok := jsoneventstest.FindByType(events, plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventExecutorHandoffPacket))
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("expected executor_handoff_packet event, got: %s", stdout.String()))
	gomega.Expect(handoff["packet_state"]).To(gomega.Equal(string(plancontract.ExecutorHandoffStateInherited)), fmt.Sprintf("expected inherited handoff packet, got %#v", handoff))
	gomega.Expect(handoff["reason"]).To(gomega.Equal("fresh_graph_view"), fmt.Sprintf("expected inherited handoff packet, got %#v", handoff))
	gomega.Expect(stdout.String()).To(gomega.ContainSubstring(`"pack_id":"`+key.ID()+`"`), fmt.Sprintf("expected pack_id %s in event, got: %s", key.ID(), stdout.String()))
	gomega.Expect(stdout.String()).To(gomega.ContainSubstring(`"candidate_files":1`), fmt.Sprintf("expected candidate_files:1 in event, got: %s", stdout.String()))

})
var _ = ginkgo.It("run executor phase emits executor pack miss on unknown pack id", func

// pack_id that doesn't match any graph_views row → executor_pack_miss
// event so downstream consumers know the planner's context wasn't
// available (rather than silently degrading).
() {

	root := ginkgo.GinkgoT().TempDir()
	gomega.Expect(os.WriteFile(filepath.Join(root, "a.txt"), []byte("alpha\n"), 0o644)).To(gomega.Succeed())

	plan := planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		PackID:        "deadbeefcafef00d",
		Edits: []planartifact.PlanEdit{
			{Path: "a.txt", OldText: "alpha", NewText: "ALPHA"},
		},
	}
	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}

	var stdout bytes.Buffer

	if _, err := runExecutorPhaseForTest(cfg, plan, planexecute.ExecutorPhaseOptions{RequiredEditPaths: nil, AllowedEditPaths: nil, VerificationCommands: nil, JSONEvents: true, Stdout: &stdout}); err != nil {
		ginkgo.Fail(fmt.Sprintf("executor phase failed: %v", err))
	}
	gomega.Expect(stdout.String()).To(gomega.ContainSubstring(plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventExecutorPackMiss)), fmt.Sprintf("expected executor_pack_miss JSON event when pack_id is unknown, got: %s", stdout.String()))

	events := jsoneventstest.DecodeLines(ginkgo.GinkgoT(), stdout.String())
	handoff, ok := jsoneventstest.FindByType(events, plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventExecutorHandoffPacket))
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("expected missing handoff packet, got %#v in %s", handoff, stdout.String()))
	gomega.Expect(handoff["packet_state"]).To(gomega.Equal(string(plancontract.ExecutorHandoffStateMissing)), fmt.Sprintf("expected missing handoff packet, got %#v in %s", handoff, stdout.String()))
	gomega.Expect(handoff["reason"]).To(gomega.Equal("cache_miss"), fmt.Sprintf("expected missing handoff packet, got %#v in %s", handoff, stdout.String()))

})
var _ = ginkgo.It("run executor phase emits missing packet handoff without plan packet", func() {
	root := ginkgo.GinkgoT().TempDir()
	gomega.Expect(os.WriteFile(filepath.Join(root, "a.txt"), []byte("alpha\n"), 0o644)).To(gomega.Succeed())

	plan := planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		PlanID:        "plan-missing-packet",
		Handoff:       planartifact.PlanArtifactHandoff{ID: "handoff-1", Status: "ready", Ready: true},
		Edits:         []planartifact.PlanEdit{{Path: "a.txt", OldText: "alpha", NewText: "ALPHA"}},
	}
	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}

	var stdout bytes.Buffer

	if _, err := runExecutorPhaseForTest(cfg, plan, planexecute.ExecutorPhaseOptions{RequiredEditPaths: nil, AllowedEditPaths: nil, VerificationCommands: nil, JSONEvents: true, Stdout: &stdout}); err != nil {
		ginkgo.Fail(fmt.Sprintf("executor phase failed: %v", err))
	}

	events := jsoneventstest.DecodeLines(ginkgo.GinkgoT(), stdout.String())
	handoff, ok := jsoneventstest.FindByType(events, plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventExecutorHandoffPacket))
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("expected executor_handoff_packet event, got %s", stdout.String()))
	gomega.Expect(handoff["packet_state"]).To(gomega.Equal(string(plancontract.ExecutorHandoffStateMissing)), fmt.Sprintf("expected missing packet state, got %#v", handoff))
	gomega.Expect(handoff["reason"]).To(gomega.Equal("missing_packet_id"), fmt.Sprintf("expected missing packet state, got %#v", handoff))

})
var _ = ginkgo.It("run executor phase blocks partial approval scope drift", func() {
	root := ginkgo.GinkgoT().TempDir()
	gomega.Expect(os.WriteFile(filepath.Join(root, "a.txt"), []byte("alpha\n"), 0o644)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(filepath.Join(root, "b.txt"), []byte("beta\n"), 0o644)).To(gomega.Succeed())

	plan := planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		PlanID:        "plan raw_prompt customer-acme token=/home/example/private/client",
		Task: planartifact.PlanArtifactTask{
			Summary:               "Block pending partial approval scope",
			ClientClassifications: []string{"finance"},
		},
		Paths: planartifact.PlanArtifactPaths{Editable: []string{"a.txt", "b.txt"}, Changed: []string{"b.txt"}},
		Approval: planartifact.PlanArtifactApproval{
			Required:            true,
			Status:              "partial",
			ScopeID:             "scope raw_prompt customer-acme token=/home/example/private/client",
			ApprovedPaths:       []string{"a.txt"},
			PendingPaths:        []string{"b.txt"},
			ExecutionPermission: "execute_approved_scope",
		},
		Handoff: planartifact.PlanArtifactHandoff{ID: "handoff raw_prompt customer-acme", Status: "ready", Ready: true},
		Edits:   []planartifact.PlanEdit{{Path: "b.txt", OldText: "beta", NewText: "BETA"}},
	}
	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}

	var stdout bytes.Buffer

	if _, err := runExecutorPhaseForTest(cfg, plan, planexecute.ExecutorPhaseOptions{RequiredEditPaths: nil, AllowedEditPaths: nil, VerificationCommands: nil, JSONEvents: true, Stdout: &stdout}); err == nil {
		ginkgo.Fail(fmt.Sprintf("executor should block pending partial approval scope"))
	}

	events := jsoneventstest.DecodeLines(ginkgo.GinkgoT(), stdout.String())
	drift, ok := jsoneventstest.FindByType(events, plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventPlanScopeDrift))
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("expected plan_scope_drift event, got %s", stdout.String()))
	gomega.Expect(drift["reason"]).To(gomega.Equal("plan edit path is still pending approval"), fmt.Sprintf("unexpected drift event: %#v", drift))
	gomega.Expect(drift["path"]).To(gomega.Equal("b.txt"), fmt.Sprintf("unexpected drift event: %#v", drift))
	gomega.Expect(drift["handoff_id"]).NotTo(gomega.Equal(""), fmt.Sprintf("drift event should carry stable governance ids: %#v", drift))
	gomega.Expect(drift["approval_scope_id"]).NotTo(gomega.Equal(""), fmt.Sprintf("drift event should carry stable governance ids: %#v", drift))

	handoff, ok := jsoneventstest.FindByType(events, plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventExecutorHandoffPacket))
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("expected executor_handoff_packet event, got %s", stdout.String()))
	gomega.Expect(handoff["approval_execution_permission"]).To(gomega.Equal("execute_approved_scope"), fmt.Sprintf("handoff event should carry partial approval counters, got %#v", handoff))
	gomega.Expect(handoff["approved_path_count"]).To(gomega.Equal(float64(1)), fmt.Sprintf("handoff event should carry partial approval counters, got %#v", handoff))
	gomega.Expect(handoff["pending_path_count"]).To(gomega.Equal(float64(1)), fmt.Sprintf("handoff event should carry partial approval counters, got %#v", handoff))

	body, err := os.ReadFile(filepath.Join(root, "b.txt"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read b.txt: %v", err))
	gomega.Expect(string(body)).To(gomega.Equal("beta\n"), fmt.Sprintf("blocked drift should not mutate pending file, got %q", body))

	for _, leaked := range []string{"raw_prompt", "customer-acme", "customer_acme", "token=", "/home/example/private"} {
		gomega.Expect(stdout.String()).NotTo(gomega.ContainSubstring(leaked), fmt.Sprintf("drift event leaked sensitive marker %q: %s", leaked, stdout.String()))

	}
})
var _ = ginkgo.It("run executor phase ignores legacy pack artifact without graph view", func() {
	root := ginkgo.GinkgoT().TempDir()
	gomega.Expect(os.WriteFile(filepath.Join(root, "a.txt"), []byte("alpha\n"), 0o644)).To(gomega.Succeed())

	packID := "deadc0decafef00d"
	legacyPacket := comprehension.PackResult{
		PackID:         packID,
		IndexID:        "idx-old",
		Root:           root,
		Task:           "stale packet",
		Mode:           "plan",
		Fresh:          false,
		CandidateFiles: []comprehension.PackFile{{Path: "a.txt", Role: "editable"}},
	}
	legacyPath := filepath.Join(root, ".idiom", "comprehension", "views", packID+".json")
	gomega.Expect(os.MkdirAll(filepath.Dir(legacyPath), 0o755)).To(gomega.Succeed())

	body, err := json.MarshalIndent(legacyPacket, "", "  ")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal legacy packet: %v", err))
	gomega.Expect(os.WriteFile(legacyPath, append(body, '\n'), 0o644)).To(gomega.Succeed())

	plan := planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		PlanID:        "plan-stale-packet",
		PackID:        packID,
		Packet:        planartifact.PlanArtifactPacket{PackID: packID, Status: "available", Freshness: "fresh"},
		Handoff:       planartifact.PlanArtifactHandoff{ID: "handoff-1", Status: "ready", Ready: true},
		Edits:         []planartifact.PlanEdit{{Path: "a.txt", OldText: "alpha", NewText: "ALPHA"}},
	}
	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}

	var stdout bytes.Buffer

	if _, err := runExecutorPhaseForTest(cfg, plan, planexecute.ExecutorPhaseOptions{RequiredEditPaths: nil, AllowedEditPaths: nil, VerificationCommands: nil, JSONEvents: true, Stdout: &stdout}); err != nil {
		ginkgo.Fail(fmt.Sprintf("executor phase failed: %v", err))
	}

	events := jsoneventstest.DecodeLines(ginkgo.GinkgoT(), stdout.String())
	handoff, ok := jsoneventstest.FindByType(events, plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventExecutorHandoffPacket))
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("expected executor_handoff_packet event, got %s", stdout.String()))
	gomega.Expect(handoff["packet_state"]).To(gomega.Equal(string(plancontract.ExecutorHandoffStateMissing)), fmt.Sprintf("expected graph cache miss despite legacy artifact, got %#v", handoff))
	gomega.Expect(handoff["reason"]).To(gomega.Equal("cache_miss"), fmt.Sprintf("expected graph cache miss despite legacy artifact, got %#v", handoff))
	gomega.Expect(handoff["fresh"]).To(gomega.Equal(false), fmt.Sprintf("expected graph cache miss despite legacy artifact, got %#v", handoff))

	if _, ok := jsoneventstest.FindByType(events, plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventExecutorPackMiss)); !ok {
		ginkgo.Fail(fmt.Sprintf("expected executor_pack_miss event, got %s", stdout.String()))
	}
	if _, ok := jsoneventstest.FindByType(events, plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventExecutorPacketStale)); ok {
		ginkgo.Fail(fmt.Sprintf("legacy artifact must not emit stale packet event, got %s", stdout.String()))
	}
})
var _ = ginkgo.It("run executor phase emits stale graph view packet without legacy artifact", func() {
	root := ginkgo.GinkgoT().TempDir()
	gomega.Expect(os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/stalehandoff\n\ngo 1.22\n"), 0o644)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(filepath.Join(root, "a.go"), []byte("package stalehandoff\n\nfunc A() string { return \"alpha\" }\n"), 0o644)).To(gomega.Succeed())

	build, err := comprehension.Build(ginkgo.GinkgoT().Context(), comprehension.BuildOptions{Root: root, Language: "go"})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("Build: %v", err))

	key := comprehension.ViewKey{IndexID: build.IndexID, Task: "stale graph packet", Mode: "plan", Query: comprehension.ViewQueryInputs{Path: "a.go"}}
	gomega.Expect(comprehension.SaveView(root, key, comprehension.PackResult{
		PackID:         key.ID(),
		IndexID:        build.IndexID,
		Root:           root,
		Task:           "stale graph packet",
		Mode:           "plan",
		Fresh:          true,
		CandidateFiles: []comprehension.PackFile{{Path: "a.go", Role: "editable"}},
	})).To(gomega.Succeed())

	if n, err := comprehension.MarkAllViewsStale(root); err != nil || n != 1 {
		ginkgo.Fail(fmt.Sprintf("MarkAllViewsStale n=%d err=%v", n, err))
	}

	if _, err := os.Stat(filepath.Join(root, ".idiom", "comprehension", "views", key.ID()+".json")); err == nil {
		ginkgo.Fail(fmt.Sprintf("legacy view artifact should not exist for graph-row stale test"))
	} else if !os.IsNotExist(err) {
		ginkgo.Fail(fmt.Sprintf("stat legacy view artifact: %v", err))
	}

	plan := planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		PlanID:        "plan-stale-graph-row",
		PackID:        key.ID(),
		Packet:        planartifact.PlanArtifactPacket{PackID: key.ID(), Status: "available", Freshness: "fresh"},
		Handoff:       planartifact.PlanArtifactHandoff{ID: "handoff-1", Status: "ready", Ready: true},
		Edits:         []planartifact.PlanEdit{{Path: "a.go", OldText: "alpha", NewText: "ALPHA"}},
	}
	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}

	var stdout bytes.Buffer

	if _, err := runExecutorPhaseForTest(cfg, plan, planexecute.ExecutorPhaseOptions{RequiredEditPaths: nil, AllowedEditPaths: nil, VerificationCommands: nil, JSONEvents: true, Stdout: &stdout}); err != nil {
		ginkgo.Fail(fmt.Sprintf("executor phase failed: %v", err))
	}

	events := jsoneventstest.DecodeLines(ginkgo.GinkgoT(), stdout.String())
	handoff, ok := jsoneventstest.FindByType(events, plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventExecutorHandoffPacket))
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("expected executor_handoff_packet event, got %s", stdout.String()))
	gomega.Expect(handoff["packet_state"]).To(gomega.Equal(string(plancontract.ExecutorHandoffStateStale)), fmt.Sprintf("expected stale graph-view packet state, got %#v", handoff))
	gomega.Expect(handoff["reason"]).To(gomega.Equal("stale_graph_view"), fmt.Sprintf("expected stale graph-view packet state, got %#v", handoff))
	gomega.Expect(handoff["fresh"]).To(gomega.Equal(false), fmt.Sprintf("expected stale graph-view packet state, got %#v", handoff))
	gomega.Expect(handoff["candidate_files"]).To(gomega.Equal(float64(1)), fmt.Sprintf("expected stale graph row to preserve candidate count, got %#v", handoff))

	if _, ok := jsoneventstest.FindByType(events, plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventExecutorPacketStale)); !ok {
		ginkgo.Fail(fmt.Sprintf("expected executor_packet_stale compatibility event, got %s", stdout.String()))
	}
})
var _ = ginkgo.It("run executor phase emits stale for non current graph view packet without legacy artifact", func() {
	root := ginkgo.GinkgoT().TempDir()
	gomega.Expect(os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/noncurrenthandoff\n\ngo 1.22\n"), 0o644)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(filepath.Join(root, "a.go"), []byte("package noncurrenthandoff\n\nfunc A() string { return \"alpha\" }\n"), 0o644)).To(gomega.Succeed())

	if _, err := comprehension.Build(ginkgo.GinkgoT().Context(), comprehension.BuildOptions{Root: root, Language: "go"}); err != nil {
		ginkgo.Fail(fmt.Sprintf("Build: %v", err))
	}

	key := comprehension.ViewKey{IndexID: "old-index", Task: "non-current graph packet", Mode: "plan", Query: comprehension.ViewQueryInputs{Path: "a.go"}}
	gomega.Expect(comprehension.SaveView(root, key, comprehension.PackResult{
		PackID:         key.ID(),
		IndexID:        "old-index",
		Root:           root,
		Task:           "non-current graph packet",
		Mode:           "plan",
		Fresh:          true,
		CandidateFiles: []comprehension.PackFile{{Path: "a.go", Role: "editable"}},
	})).To(gomega.Succeed())

	if _, err := os.Stat(filepath.Join(root, ".idiom", "comprehension", "views", key.ID()+".json")); err == nil {
		ginkgo.Fail(fmt.Sprintf("legacy view artifact should not exist for non-current graph-row test"))
	} else if !os.IsNotExist(err) {
		ginkgo.Fail(fmt.Sprintf("stat legacy view artifact: %v", err))
	}

	plan := planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		PlanID:        "plan-non-current-graph-row",
		PackID:        key.ID(),
		Packet:        planartifact.PlanArtifactPacket{PackID: key.ID(), Status: "available", Freshness: "fresh"},
		Handoff:       planartifact.PlanArtifactHandoff{ID: "handoff-1", Status: "ready", Ready: true},
		Edits:         []planartifact.PlanEdit{{Path: "a.go", OldText: "alpha", NewText: "ALPHA"}},
	}
	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}

	var stdout bytes.Buffer

	if _, err := runExecutorPhaseForTest(cfg, plan, planexecute.ExecutorPhaseOptions{RequiredEditPaths: nil, AllowedEditPaths: nil, VerificationCommands: nil, JSONEvents: true, Stdout: &stdout}); err != nil {
		ginkgo.Fail(fmt.Sprintf("executor phase failed: %v", err))
	}

	events := jsoneventstest.DecodeLines(ginkgo.GinkgoT(), stdout.String())
	handoff, ok := jsoneventstest.FindByType(events, plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventExecutorHandoffPacket))
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("expected executor_handoff_packet event, got %s", stdout.String()))
	gomega.Expect(handoff["packet_state"]).To(gomega.Equal(string(plancontract.ExecutorHandoffStateStale)), fmt.Sprintf("expected stale non-current graph-view packet state, got %#v", handoff))
	gomega.Expect(handoff["reason"]).To(gomega.Equal("stale_graph_view"), fmt.Sprintf("expected stale non-current graph-view packet state, got %#v", handoff))
	gomega.Expect(handoff["fresh"]).To(gomega.Equal(false), fmt.Sprintf("expected stale non-current graph-view packet state, got %#v", handoff))

	if _, ok := jsoneventstest.FindByType(events, plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventExecutorPackMiss)); ok {
		ginkgo.Fail(fmt.Sprintf("non-current graph row should not collapse to executor_pack_miss, got %s", stdout.String()))
	}
})
var _ = ginkgo.It("run executor phase emits overridden packet handoff and redacts sensitive i ds", func() {
	root := ginkgo.GinkgoT().TempDir()
	gomega.Expect(os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/override\n\ngo 1.22\n"), 0o644)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(filepath.Join(root, "pkg.go"), []byte("package override\n"), 0o644)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(filepath.Join(root, "a.txt"), []byte("alpha\n"), 0o644)).To(gomega.Succeed())

	build, err := comprehension.Build(ginkgo.GinkgoT().Context(), comprehension.BuildOptions{Root: root, Language: "go"})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("Build: %v", err))

	key := comprehension.ViewKey{IndexID: build.IndexID, Task: "override packet", Mode: "plan"}
	packID := key.ID()
	gomega.Expect(comprehension.SaveView(root, key, comprehension.PackResult{
		PackID:         packID,
		IndexID:        build.IndexID,
		Root:           root,
		Task:           "override packet",
		Mode:           "plan",
		Fresh:          true,
		CandidateFiles: []comprehension.PackFile{{Path: "a.txt", Role: "editable"}},
	})).To(gomega.Succeed())

	plan := planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		PlanID:        "plan raw_prompt customer-acme token=/home/example/private/client",
		PackID:        packID,
		Packet:        planartifact.PlanArtifactPacket{PackID: "feedfacecafebeef", Status: "available", Freshness: "fresh"},
		Handoff:       planartifact.PlanArtifactHandoff{ID: "handoff raw_prompt customer-acme token", Status: "ready", Ready: true},
		Edits:         []planartifact.PlanEdit{{Path: "a.txt", OldText: "alpha", NewText: "ALPHA"}},
	}
	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}

	var stdout bytes.Buffer

	if _, err := runExecutorPhaseForTest(cfg, plan, planexecute.ExecutorPhaseOptions{RequiredEditPaths: nil, AllowedEditPaths: nil, VerificationCommands: nil, JSONEvents: true, Stdout: &stdout}); err != nil {
		ginkgo.Fail(fmt.Sprintf("executor phase failed: %v", err))
	}

	events := jsoneventstest.DecodeLines(ginkgo.GinkgoT(), stdout.String())
	handoff, ok := jsoneventstest.FindByType(events, plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventExecutorHandoffPacket))
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("expected executor_handoff_packet event, got %s", stdout.String()))
	gomega.Expect(handoff["packet_state"]).To(gomega.Equal(string(plancontract.ExecutorHandoffStateOverridden)), fmt.Sprintf("expected overridden packet state, got %#v", handoff))
	gomega.Expect(handoff["reason"]).To(gomega.Equal("packet_id_override"), fmt.Sprintf("expected overridden packet state, got %#v", handoff))

	for _, leaked := range []string{"raw_prompt", "customer-acme", "customer_acme", "token=", "/home/example/private"} {
		gomega.Expect(stdout.String()).NotTo(gomega.ContainSubstring(leaked), fmt.Sprintf("handoff event leaked sensitive marker %q: %s", leaked, stdout.String()))

	}
})
var _ = ginkgo.It("plan execute review repair prompt requires behavioral checklist", func() {
	prompt := planexecute.PlanExecuteReviewRepairPrompt(
		"Implement the required behavior.",
		planartifact.PlanArtifact{
			Rationale: "change behavior",
			Edits: []planartifact.PlanEdit{
				{Path: "a.go", Purpose: "update behavior"},
			},
		},
		[]string{"a.go"},
		[]string{"a.go"},
		planexecute.ReviewRepairEvidence{
			Diff:          "diff --git a/a.go b/a.go\n",
			DiffCheckExit: 0,
			PhaseEvidence: planexecute.PhaseEvidenceBundle{
				SchemaVersion: planexecute.PhaseEvidenceSchemaVersion,
				Phase:         "review_repair",
				ChangedPaths:  []string{"a.go"},
				Packet: planexecute.PhaseEvidencePacket{
					Status:         "available",
					PackID:         "abc123",
					CandidateFiles: []planexecute.PhaseEvidenceFile{{Path: "a.go", Role: "editable", Reason: "matched task"}},
				},
				SuggestedTests: []planexecute.PhaseEvidenceTest{{Command: "go test ./...", Path: "a.go", Reason: "packet suggested test"}},
				SubmittedPlan:  []plancontract.PhaseEvidenceCheck{{Kind: "submitted_edit", Path: "a.go", Status: "applied", Summary: "edit 1/1: submitted new_text is present"}},
				SourceSanity:   []plancontract.PhaseEvidenceCheck{{Kind: "source_sanity", Path: "a.go", Status: "ok", Summary: "Source sanity passed"}},
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
		gomega.Expect(prompt).To(gomega.ContainSubstring(want), fmt.Sprintf("review-repair prompt missing %q:\n%s", want, prompt))

	}
})
var _ = ginkgo.It("plan execute review repair prompt treats failed source sanity as terminal evidence", func() {
	prompt := planexecute.PlanExecuteReviewRepairPrompt(
		"Implement the required behavior.",
		planartifact.PlanArtifact{
			Rationale: "change behavior",
			Edits: []planartifact.PlanEdit{
				{Path: "a.go", Purpose: "update behavior"},
			},
		},
		[]string{"a.go"},
		[]string{"a.go"},
		planexecute.ReviewRepairEvidence{
			Diff:          "diff --git a/a.go b/a.go\n",
			DiffCheckExit: 0,
			PhaseEvidence: planexecute.PhaseEvidenceBundle{
				SchemaVersion:   planexecute.PhaseEvidenceSchemaVersion,
				Phase:           "review_repair",
				ChangedPaths:    []string{"a.go"},
				OpenObligations: []string{"source_sanity_failed:a.go"},
				SubmittedPlan:   []plancontract.PhaseEvidenceCheck{{Kind: "submitted_edit", Path: "a.go", Status: "applied", Summary: "edit 1/1: submitted new_text is present"}},
				SourceSanity:    []plancontract.PhaseEvidenceCheck{{Kind: "source_sanity", Path: "a.go", Status: "failed", Summary: "Source sanity failed", Error: "compile failed"}},
			},
		},
	)
	for _, want := range []string{
		"failed or open obligations",
		"Treat that obligation as sufficient post-edit evidence",
		"do not perform additional reads, searches, or context refreshes",
		"Submit a corrective plan",
		"<<QUESTION>>",
		"source_sanity_failed:a.go",
	} {
		gomega.Expect(prompt).To(gomega.ContainSubstring(want), fmt.Sprintf("review-repair failed-source-sanity prompt missing %q:\n%s", want, prompt))

	}

	for _, forbidden := range []string{
		"If exact post-edit evidence is missing, make one targeted probe",
		"`context_status` (`refresh=true`)",
		"`context_query`",
		"`context_zoom`",
		"`code_navigate`",
		"one bounded `read_file` range",
	} {
		gomega.Expect(prompt).NotTo(gomega.ContainSubstring(forbidden), fmt.Sprintf("failed-source-sanity prompt must not invite discovery via %q:\n%s", forbidden, prompt))

	}
})
var _ = ginkgo.It("plan execute review repair prompt quotes original spec allowlist", func() {
	original := `# Atom

## Files To Modify

| File | Change |
|------|--------|
| ` + "`src/a.go`" + ` | MODIFY: original implementation target |
`
	prompt := planexecute.PlanExecuteReviewRepairPrompt(
		original,
		planartifact.PlanArtifact{
			Rationale: "change behavior",
			Edits: []planartifact.PlanEdit{
				{Path: "src/a.go", Purpose: "update behavior"},
			},
		},
		[]string{"src/a.go"},
		[]string{"src/a.go"},
		planexecute.ReviewRepairEvidence{Diff: "diff --git a/src/a.go b/src/a.go\n", DiffCheckExit: 0},
	)

	if paths := plancontract.RequiredEditPathsFromPrompt(prompt); len(paths) != 0 {
		ginkgo.Fail(fmt.Sprintf("original spec allowlist should be quoted reference text, got active required paths %#v\nprompt:\n%s", paths, prompt))
	}
	gomega.Expect(prompt).To(gomega.ContainSubstring("> ## Files To Modify"), fmt.Sprintf("original spec should remain visible as quoted reference text:\n%s", prompt))

})
var _ = ginkgo.It("collect review repair evidence uses executor baseline not git head", func() {
	root := initGitRepoForStaleness()
	gitCommit(root, "a.txt", "head\n", "initial")
	gomega.Expect(os.WriteFile(filepath.Join(root, "a.txt"), []byte("dirty-before-lane\n"), 0o644)).To(gomega.Succeed())

	baseline := planexecute.CaptureReviewRepairBaseline(root, []string{"a.txt"})
	gomega.Expect(os.WriteFile(filepath.Join(root, "a.txt"), []byte("after-executor\n"), 0o644)).To(gomega.Succeed())

	evidence := planexecute.CollectReviewRepairEvidence(root, []string{"a.txt"}, &baseline)
	gomega.Expect(evidence.Diff).To(gomega.ContainSubstring("-dirty-before-lane"), fmt.Sprintf("current-run diff should compare against executor baseline, got:\n%s", evidence.Diff))
	gomega.Expect(evidence.Diff).To(gomega.ContainSubstring("+after-executor"), fmt.Sprintf("current-run diff should compare against executor baseline, got:\n%s", evidence.Diff))
	gomega.Expect(evidence.Diff).NotTo(gomega.ContainSubstring("-head"), fmt.Sprintf("current-run diff must not compare against git HEAD, got:\n%s", evidence.Diff))

})
var _ = ginkgo.It("collect review repair evidence checks current run whitespace", func() {
	root := ginkgo.GinkgoT().TempDir()
	gomega.Expect(os.WriteFile(filepath.Join(root, "a.txt"), []byte("clean-before\n"), 0o644)).To(gomega.Succeed())

	baseline := planexecute.CaptureReviewRepairBaseline(root, []string{"a.txt"})
	gomega.Expect(os.WriteFile(filepath.Join(root, "a.txt"), []byte("clean-before\nbad   \n"), 0o644)).To(gomega.Succeed())

	evidence := planexecute.CollectReviewRepairEvidence(root, []string{"a.txt"}, &baseline)
	gomega.Expect(evidence.DiffCheckExit).To(gomega.Equal(1), fmt.Sprintf("trailing whitespace should fail current-run hygiene, got exit %d diff=%s", evidence.DiffCheckExit, evidence.Diff))
	gomega.Expect(evidence.DiffCheck).To(gomega.ContainSubstring("a.txt:2"), fmt.Sprintf("whitespace finding should include current-run path and line, got:\n%s", evidence.DiffCheck))

})
var _ = ginkgo.It("auto repair review patch hygiene trims only current run lines", func() {
	root := ginkgo.GinkgoT().TempDir()
	gomega.Expect(os.WriteFile(filepath.Join(root, "a.txt"), []byte("preexisting   \nclean-before\n"), 0o644)).To(gomega.Succeed())

	baseline := planexecute.CaptureReviewRepairBaseline(root, []string{"a.txt"})
	gomega.Expect(os.WriteFile(filepath.Join(root, "a.txt"), []byte("preexisting   \nclean-before\nbad   \n"), 0o644)).To(gomega.Succeed())

	evidence := planexecute.CollectReviewRepairEvidence(root, []string{"a.txt"}, &baseline)

	report, err := planexecute.AutoRepairReviewPatchHygiene(root, []string{"a.txt"}, evidence)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("auto repair patch hygiene: %v", err))
	gomega.Expect(report.Repaired).To(gomega.BeTrue(), fmt.Sprintf("expected one repaired line, got %#v", report))
	gomega.Expect(report.Lines).To(gomega.Equal(1), fmt.Sprintf("expected one repaired line, got %#v", report))

	got, err := os.ReadFile(filepath.Join(root, "a.txt"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read repaired file: %v", err))
	gomega.Expect(string(got)).To(gomega.Equal("preexisting   \nclean-before\nbad\n"), fmt.Sprintf("auto repair should trim only current-run trailing whitespace, got %q", string(got)))

})
var _ = ginkgo.It("run review repair phase auto repairs patch hygiene before prompt", func() {
	root := ginkgo.GinkgoT().TempDir()
	gomega.Expect(os.WriteFile(filepath.Join(root, "a.txt"), []byte("clean-before\n"), 0o644)).To(gomega.Succeed())

	baseline := planexecute.CaptureReviewRepairBaseline(root, []string{"a.txt"})
	gomega.Expect(os.WriteFile(filepath.Join(root, "a.txt"), []byte("clean-before\nbad   \n"), 0o644)).To(gomega.Succeed())

	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}
	plan := planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		Edits: []planartifact.PlanEdit{
			{Path: "a.txt", OldText: "clean-before\n", NewText: "clean-before\nbad   \n", Purpose: "seed changed path"},
		},
	}

	var (
		stdout    bytes.Buffer
		gotPrompt string
	)

	report, err := runReviewRepairPhaseForTest(
		cfg,
		"Add bad line.",
		plan,
		nil,
		&baseline,
		planexecute.ExecutorPhaseReport{},
		planexecute.Options{},
		&stdout,
		func(_ config.Config, prompt string, _ cmdruntime.RunOptions, _ io.Writer) (string, error) {
			gotPrompt = prompt
			return "Decision: pass\n\nVerification Checklist:\n- Required behavior: bad line is present.\n- Patch hygiene: clean after mechanical repair.", nil
		},
		nil,
	)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("review-repair should accept auto-repaired hygiene: %v", err))
	gomega.Expect(report.AutoRepairs).To(gomega.Equal(1), fmt.Sprintf("expected one mechanical repair, got %#v", report))
	gomega.Expect(gotPrompt).To(gomega.ContainSubstring("patch_hygiene: status=passed"), fmt.Sprintf("review prompt should receive repaired patch hygiene evidence, got:\n%s", gotPrompt))
	gomega.Expect(gotPrompt).NotTo(gomega.ContainSubstring("submitted_edit_pending"), fmt.Sprintf("mechanical whitespace repair should not make submitted edit look pending:\n%s", gotPrompt))
	gomega.Expect(gotPrompt).NotTo(gomega.ContainSubstring("submitted_edit_unknown"), fmt.Sprintf("mechanical whitespace repair should not make submitted edit look pending:\n%s", gotPrompt))

	got, err := os.ReadFile(filepath.Join(root, "a.txt"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read repaired file: %v", err))
	gomega.Expect(string(got)).To(gomega.Equal("clean-before\nbad\n"), fmt.Sprintf("expected repaired file, got %q", string(got)))

})
var _ = ginkgo.It("run review repair phase rejects pass without checklist", func() {
	root := ginkgo.GinkgoT().TempDir()
	gomega.Expect(os.WriteFile(filepath.Join(root, "a.txt"), []byte("bad\n"), 0o644)).To(gomega.Succeed())

	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}
	plan := planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		Edits: []planartifact.PlanEdit{
			{Path: "a.txt", OldText: "old", NewText: "bad", Purpose: "seed changed path"},
		},
	}

	var stdout bytes.Buffer

	_, err := runReviewRepairPhaseForTest(
		cfg,
		"Review behavior.",
		plan,
		nil,
		nil,
		planexecute.ExecutorPhaseReport{},
		planexecute.Options{},
		&stdout,
		func(_ config.Config, _ string, _ cmdruntime.RunOptions, _ io.Writer) (string, error) {
			return "Decision: pass\nNo issues.", nil
		},
		nil,
	)
	gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprintf("expected inconclusive review without checklist"))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("Verification Checklist"), fmt.Sprintf("error should require checklist, got %v", err))

})
var _ = ginkgo.It("run review repair phase rejects recovery template as pass", func() {
	root := ginkgo.GinkgoT().TempDir()
	gomega.Expect(os.WriteFile(filepath.Join(root, "a.txt"), []byte("good\n"), 0o644)).To(gomega.Succeed())

	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}
	plan := planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		Edits: []planartifact.PlanEdit{
			{Path: "a.txt", OldText: "bad", NewText: "good", Purpose: "seed changed path"},
		},
	}

	var stdout bytes.Buffer

	_, err := runReviewRepairPhaseForTest(
		cfg,
		"Review behavior.",
		plan,
		nil,
		nil,
		planexecute.ExecutorPhaseReport{},
		planexecute.Options{},
		&stdout,
		func(_ config.Config, _ string, _ cmdruntime.RunOptions, _ io.Writer) (string, error) {
			return "The completion gate rejected the review-repair response.\n\nPass shape:\nDecision: pass\n\nVerification Checklist:\n- Required behavior: <checked fact>.\n\nDo not add prose before or after the chosen terminal shape.", nil
		},
		nil,
	)
	gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprintf("expected recovery template text not to count as a review pass"))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("Decision: pass"), fmt.Sprintf("error should require an actual terminal pass shape, got %v", err))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("Verification Checklist"), fmt.Sprintf("error should require an actual terminal pass shape, got %v", err))

})
var _ = ginkgo.It("run review repair phase rejects contradictory pass", func() {
	root := ginkgo.GinkgoT().TempDir()
	gomega.Expect(os.WriteFile(filepath.Join(root, "a.txt"), []byte("partial\n"), 0o644)).To(gomega.Succeed())

	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}
	plan := planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		Edits: []planartifact.PlanEdit{
			{Path: "a.txt", OldText: "old", NewText: "partial", Purpose: "seed changed path"},
		},
	}

	var stdout bytes.Buffer

	_, err := runReviewRepairPhaseForTest(
		cfg,
		"Review behavior.",
		plan,
		nil,
		nil,
		planexecute.ExecutorPhaseReport{},
		planexecute.Options{
			JSONEvents: true,
			EditSetObligations: plancontract.EditSetObligations{}.With(
				plancontract.EditSetObligationKindTestEdit,
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
	gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprintf("expected contradictory pass rejection"))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("contradictory"), fmt.Sprintf("expected contradiction reason, got %v", err))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("unresolved_failure_claim"), fmt.Sprintf("expected contradiction reason, got %v", err))

	events := jsoneventstest.DecodeLines(ginkgo.GinkgoT(), stdout.String())
	if _, ok := jsoneventstest.FindByType(events, plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventReviewRepairPassRejected)); !ok {
		ginkgo.Fail(fmt.Sprintf("expected review_repair_pass_rejected event, got %s", stdout.String()))
	}
})
var _ = ginkgo.It("run review repair phase rejects pass that admits command was not run", func() {
	root := ginkgo.GinkgoT().TempDir()
	gomega.Expect(os.WriteFile(filepath.Join(root, "a.txt"), []byte("after\n"), 0o644)).To(gomega.Succeed())

	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}
	plan := planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		Edits: []planartifact.PlanEdit{
			{Path: "a.txt", OldText: "before", NewText: "after", Purpose: "seed changed path"},
		},
	}

	var stdout bytes.Buffer

	_, err := runReviewRepairPhaseForTest(
		cfg,
		"Implement behavior and run focused tests.",
		plan,
		nil,
		nil,
		planexecute.ExecutorPhaseReport{},
		planexecute.Options{JSONEvents: true},
		&stdout,
		func(_ config.Config, _ string, _ cmdruntime.RunOptions, _ io.Writer) (string, error) {
			return "Decision: pass\n\nVerification Checklist:\n- Required behavior: diff looks scoped.\n- Test or command evidence: We didn't run `go test ./cmd/idiom`.", nil
		},
		nil,
	)
	gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprintf("expected missing command evidence admission to reject review pass"))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("missing_command_evidence_claim"), fmt.Sprintf("expected missing command evidence rejection, got %v", err))

	events := jsoneventstest.DecodeLines(ginkgo.GinkgoT(), stdout.String())
	event, ok := jsoneventstest.FindByType(events, plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventReviewRepairPassRejected))
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("expected review_repair_pass_rejected event, got %s", stdout.String()))
	gomega.Expect(event["reason"]).To(gomega.Equal("missing_command_evidence_claim"), fmt.Sprintf("expected missing_command_evidence_claim reason, got %#v", event))

})
var _ = ginkgo.It("run review repair phase rejects pass when requested test edit missing", func() {
	root := ginkgo.GinkgoT().TempDir()
	gomega.Expect(os.WriteFile(filepath.Join(root, "pkg.go"), []byte("package demo\n\nfunc Hardened() bool { return true }\n"), 0o644)).To(gomega.Succeed())

	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}
	plan := planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		Edits: []planartifact.PlanEdit{
			{Path: "pkg.go", OldText: "return false", NewText: "return true", Purpose: "harden behavior"},
		},
	}

	var (
		stdout    bytes.Buffer
		gotPrompt string
	)

	_, err := runReviewRepairPhaseForTest(
		cfg,
		"Implement the hardening and add regression tests proving the edge case is rejected.",
		plan,
		nil,
		nil,
		planexecute.ExecutorPhaseReport{},
		planexecute.Options{
			JSONEvents: true,
			EditSetObligations: plancontract.EditSetObligations{}.With(
				plancontract.EditSetObligationKindTestEdit,
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
	gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprintf("expected missing requested test edit to reject review pass"))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("missing_requested_test_edit"), fmt.Sprintf("expected missing test edit rejection, got %v", err))
	gomega.Expect(gotPrompt).To(gomega.ContainSubstring("requested_test_edit_missing"), fmt.Sprintf("review prompt should expose requested test edit obligation, got:\n%s", gotPrompt))

	events := jsoneventstest.DecodeLines(ginkgo.GinkgoT(), stdout.String())
	event, ok := jsoneventstest.FindByType(events, plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventReviewRepairPassRejected))
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("expected review_repair_pass_rejected event, got %s", stdout.String()))
	gomega.Expect(event["reason"]).To(gomega.Equal("missing_requested_test_edit"), fmt.Sprintf("expected missing_requested_test_edit reason, got %#v", event))

})
var _ = ginkgo.It("run review repair phase rejects pass when requested implementation edit missing", func() {
	root := ginkgo.GinkgoT().TempDir()
	gomega.Expect(os.WriteFile(filepath.Join(root, "feature_test.go"), []byte("package demo\n\nfunc TestEnabled(t *testing.T) {\n\tif !Enabled() {\n\t\tt.Fatal(\"disabled\")\n\t}\n}\n"), 0o644)).To(gomega.Succeed())

	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}
	plan := planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		Edits: []planartifact.PlanEdit{
			{Path: "feature_test.go", OldText: "func TestEnabled(t *testing.T) {}\n", NewText: "func TestEnabled(t *testing.T) {\n\tif !Enabled() {\n\t\tt.Fatal(\"disabled\")\n\t}\n}\n", Purpose: "add feature test"},
		},
	}

	var (
		stdout    bytes.Buffer
		gotPrompt string
	)

	_, err := runReviewRepairPhaseForTest(
		cfg,
		"Implement the feature. Start by adding failing tests, then implement.",
		plan,
		nil,
		nil,
		planexecute.ExecutorPhaseReport{},
		planexecute.Options{
			JSONEvents: true,
			EditSetObligations: plancontract.EditSetObligations{}.With(
				plancontract.EditSetObligationKindImplementationEdit,
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
	gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprintf("expected missing requested implementation edit to reject review pass"))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("missing_requested_implementation_edit"), fmt.Sprintf("expected missing implementation edit rejection, got %v", err))
	gomega.Expect(gotPrompt).To(gomega.ContainSubstring("requested_implementation_edit_missing"), fmt.Sprintf("review prompt should expose requested implementation edit obligation, got:\n%s", gotPrompt))

	events := jsoneventstest.DecodeLines(ginkgo.GinkgoT(), stdout.String())
	event, ok := jsoneventstest.FindByType(events, plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventReviewRepairPassRejected))
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("expected review_repair_pass_rejected event, got %s", stdout.String()))
	gomega.Expect(event["reason"]).To(gomega.Equal("missing_requested_implementation_edit"), fmt.Sprintf("expected missing_requested_implementation_edit reason, got %#v", event))

})
var _ = ginkgo.It("run review repair phase rejects pass when verification failed", func() {
	root := ginkgo.GinkgoT().TempDir()
	gomega.Expect(os.WriteFile(filepath.Join(root, "pkg.go"), []byte("package demo\n\nfunc Value() int { return 2 }\n"), 0o644)).To(gomega.Succeed())

	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}
	plan := planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		Edits: []planartifact.PlanEdit{
			{Path: "pkg.go", OldText: "return 1", NewText: "return 2", Purpose: "change behavior"},
		},
	}
	command := "go test ./... -run 'TestValue' -count=1"

	var (
		stdout    bytes.Buffer
		gotPrompt string
	)

	_, err := runReviewRepairPhaseForTest(
		cfg,
		"Update Value. Make this pass: `"+command+"`.",
		plan,
		nil,
		nil,
		planexecute.ExecutorPhaseReport{Total: 1, Succeeded: 1, Verification: []plancontract.PhaseEvidenceCheck{{
			Kind:    "verification",
			Command: command,
			Status:  "failed",
			Error:   "TestValue failed",
		}}},
		planexecute.Options{JSONEvents: true},
		&stdout,
		func(_ config.Config, prompt string, _ cmdruntime.RunOptions, _ io.Writer) (string, error) {
			gotPrompt = prompt
			return "Decision: pass\n\nVerification Checklist:\n- Requested verification: acceptable.", nil
		},
		nil,
	)
	gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprintf("expected failed requested verification to reject review pass"))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("verification_failed"), fmt.Sprintf("expected verification_failed rejection, got %v", err))
	gomega.Expect(gotPrompt).To(gomega.ContainSubstring("verification_failed:"+command), fmt.Sprintf("review prompt should expose failed verification obligation, got:\n%s", gotPrompt))

	events := jsoneventstest.DecodeLines(ginkgo.GinkgoT(), stdout.String())
	event, ok := jsoneventstest.FindByType(events, plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventReviewRepairPassRejected))
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("expected review_repair_pass_rejected event, got %s", stdout.String()))
	gomega.Expect(event["reason"]).To(gomega.Equal("verification_failed"), fmt.Sprintf("expected verification_failed reason, got %#v", event))

})
var _ = ginkgo.It("run review repair phase retries contradictory pass into corrective plan", func() {
	root := ginkgo.GinkgoT().TempDir()
	path := filepath.Join(root, "a.txt")
	gomega.Expect(os.WriteFile(path, []byte("value=bad\n"), 0o644)).To(gomega.Succeed())

	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}
	plan := planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		Edits: []planartifact.PlanEdit{
			{Path: "a.txt", OldText: "value=good", NewText: "value=bad", Purpose: "introduce bad value"},
		},
	}

	var stdout bytes.Buffer

	attempts := 0

	report, err := runReviewRepairPhaseForTest(
		cfg,
		"Keep a.txt good.",
		plan,
		nil,
		nil,
		planexecute.ExecutorPhaseReport{Total: 1, Succeeded: 1, Verification: []plancontract.PhaseEvidenceCheck{{
			Kind:    "verification",
			Command: "check a.txt",
			Status:  "failed",
			Error:   "value was bad",
		}}},
		planexecute.Options{JSONEvents: true},
		&stdout,
		func(cfg config.Config, prompt string, opts cmdruntime.RunOptions, _ io.Writer) (string, error) {
			attempts++
			gomega.Expect(opts.TaskFamily).To(gomega.Equal(string(autonomy.TaskFamilyPlanThenExecuteReview)), fmt.Sprintf("unexpected task family %q", opts.TaskFamily))

			if attempts == 1 {
				gomega.Expect(prompt).To(gomega.ContainSubstring("verification_failed:check a.txt"), fmt.Sprintf("initial prompt should expose failed verification, got:\n%s", prompt))

				return "Decision: pass\n\nVerification Checklist:\n- Required behavior: acceptable.\n- Test or command evidence: acceptable.", nil
			}
			gomega.Expect(prompt).To(gomega.ContainSubstring("Rejected Previous Review Outcome"), fmt.Sprintf("retry prompt should forbid another pass, got:\n%s", prompt))
			gomega.Expect(prompt).To(gomega.ContainSubstring("`Decision: pass` is not an allowed terminal outcome"), fmt.Sprintf("retry prompt should forbid another pass, got:\n%s", prompt))

			writePlanExecuteTestArtifact(cfg.Workspace.Root, cfg.Workspace.SessionID, planartifact.PlanArtifact{
				SchemaVersion: planartifact.SchemaVersion,
				Created:       time.Now().UTC().Format(time.RFC3339Nano),
				SessionID:     cfg.Workspace.SessionID,
				Rationale:     "Verification Checklist:\n- Failing item: check a.txt reported value was bad.\n- Scope safety: edit only a.txt.",
				Edits: []planartifact.PlanEdit{
					{Path: "a.txt", OldText: "value=bad", NewText: "value=good", Purpose: "repair failed verification"},
				},
			})

			return "submitted repair plan", nil
		},
		nil,
	)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("expected retry corrective plan to repair, got %v", err))
	gomega.Expect(attempts).To(gomega.Equal(2), fmt.Sprintf("expected one retry after contradictory pass, got %d attempts", attempts))
	gomega.Expect(report.Status).To(gomega.Equal(plancontract.PlanExecuteReviewStatusRepaired), fmt.Sprintf("expected repaired report, got %#v", report))
	gomega.Expect(report.Repairs).To(gomega.Equal(1), fmt.Sprintf("expected repaired report, got %#v", report))

	if got := readFileString(path); got != "value=good\n" {
		ginkgo.Fail(fmt.Sprintf("expected repair to apply, got %q", got))
	}

	events := jsoneventstest.DecodeLines(ginkgo.GinkgoT(), stdout.String())
	if _, ok := jsoneventstest.FindByType(events, plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventReviewRepairRetry)); !ok {
		ginkgo.Fail(fmt.Sprintf("expected review_repair_retry event, got %s", stdout.String()))
	}
})
var _ = ginkgo.It("run review repair phase rejects corrective plan when requested test edit still missing", func() {
	root := ginkgo.GinkgoT().TempDir()
	gomega.Expect(os.WriteFile(filepath.Join(root, "pkg.go"), []byte("package demo\n\nfunc Hardened() bool { return false }\n"), 0o644)).To(gomega.Succeed())

	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}
	plan := planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		Edits: []planartifact.PlanEdit{
			{Path: "pkg.go", OldText: "return false", NewText: "return true", Purpose: "harden behavior"},
		},
	}

	var stdout bytes.Buffer

	_, err := runReviewRepairPhaseForTest(
		cfg,
		"Implement the hardening and add regression tests proving the edge case is rejected.",
		plan,
		nil,
		nil,
		planexecute.ExecutorPhaseReport{},
		planexecute.Options{
			JSONEvents:         true,
			EditSetObligations: plancontract.EditSetObligations{}.With(plancontract.EditSetObligationKindTestEdit, "test"),
		},
		&stdout,
		func(cfg config.Config, _ string, opts cmdruntime.RunOptions, _ io.Writer) (string, error) {
			gomega.Expect(opts.TaskFamily).To(gomega.Equal(string(autonomy.TaskFamilyPlanThenExecuteReview)), fmt.Sprintf("unexpected task family %q", opts.TaskFamily))

			writePlanExecuteTestArtifact(cfg.Workspace.Root, cfg.Workspace.SessionID, planartifact.PlanArtifact{
				SchemaVersion: planartifact.SchemaVersion,
				Created:       time.Now().UTC().Format(time.RFC3339Nano),
				SessionID:     cfg.Workspace.SessionID,
				Rationale:     "Verification Checklist:\n- Required behavior: hardening is present.\n",
				Edits: []planartifact.PlanEdit{
					{Path: "pkg.go", OldText: "return false", NewText: "return true", Purpose: "retry production-only hardening"},
				},
			})

			return "submitted production-only repair", nil
		},
		nil,
	)
	gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprintf("expected production-only repair plan rejection"))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("missing_requested_test_edit"), fmt.Sprintf("expected missing_requested_test_edit, got %v", err))

	if got := readFileString(filepath.Join(root, "pkg.go")); strings.Contains(got, "return true") {
		ginkgo.Fail(fmt.Sprintf("repair rejection must happen before writes, got:\n%s", got))
	}

	events := jsoneventstest.DecodeLines(ginkgo.GinkgoT(), stdout.String())
	event, ok := jsoneventstest.FindByType(events, plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventReviewRepairPlanRejected))
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("expected review_repair_plan_rejected event, got %s", stdout.String()))
	gomega.Expect(event["reason"]).To(gomega.Equal("missing_requested_test_edit"), fmt.Sprintf("expected missing_requested_test_edit reason, got %#v", event))

})
var _ = ginkgo.It("review answer pass contradiction allows negated missing coverage", func() {
	answer := strings.Join([]string{
		"Verification Checklist",
		"- Required behavior: implemented.",
		"- Scope safety: no missing test coverage and no unresolved blockers.",
		"- Non-regression: no issues found.",
		"",
		"Decision: pass",
	}, "\n")
	if reason := planexecute.ReviewAnswerPassContradictionReason(answer); reason != "" {
		ginkgo.Fail(fmt.Sprintf("clean negated checklist should not be contradictory, got %q", reason))
	}
})
var _ = ginkgo.It("review answer pass contradiction allows planner owned pending command", func() {
	answer := strings.Join([]string{
		"Verification Checklist",
		"- [x] Required behavior: implemented from the diff evidence.",
		"- [ ] npm test — planner-owned, pending outside this lane. Not marked as run.",
		"",
		"Decision: pass",
	}, "\n")
	if reason := planexecute.ReviewAnswerPassContradictionReason(answer); reason != "" {
		ginkgo.Fail(fmt.Sprintf("planner-owned pending verification should not make pass contradictory, got %q", reason))
	}
})
var _ = ginkgo.It("review answer pass contradiction rejects unverifiable required edit", func() {
	answer := strings.Join([]string{
		"Verification Checklist",
		"- Required behavior: WIRE cap tests were added.",
		"",
		"The actual test content from Required Edit 2 was not visible in the diff evidence provided.",
		"We cannot verify that the full test content was properly added.",
		"",
		"Decision: pass",
	}, "\n")
	if reason := planexecute.ReviewAnswerPassContradictionReason(answer); reason == "" {
		ginkgo.Fail(fmt.Sprintf("review pass that admits missing evidence must be contradictory"))
	}
})
var _ = ginkgo.It("review answer pass contradiction rejects placeholder checklist", func() {
	answer := strings.Join([]string{
		"Decision: pass",
		"",
		"Verification Checklist:",
		"- Required behavior: cite a checked fact from Phase/Diff Evidence.",
		"- Scope safety: cite a checked fact from changed paths and allowed repair paths.",
	}, "\n")
	if reason := planexecute.ReviewAnswerPassContradictionReason(answer); reason != "placeholder_checklist_item" {
		ginkgo.Fail(fmt.Sprintf("placeholder checklist should be contradictory, got %q", reason))
	}
})
var _ = ginkgo.It("review answer pass contradiction rejects projected verification claims", func() {
	answer := strings.Join([]string{
		"Verification Checklist",
		"- Required behavior: the new scenario file matches the requested body.",
		"- Smoke Test Execution: ./tests/run_scenarios.sh tests/scenarios/smoke/example.json should pass.",
		"- Git Diff Check: git diff --check should pass with no whitespace errors.",
		"",
		"Decision: pass",
	}, "\n")
	if reason := planexecute.ReviewAnswerPassContradictionReason(answer); reason != "unsupported_verification_projection" {
		ginkgo.Fail(fmt.Sprintf("projected verification should be contradictory, got %q", reason))
	}
})
var _ = ginkgo.It("run review repair phase uses review repair task family for pass", func() {
	root := ginkgo.GinkgoT().TempDir()
	gomega.Expect(os.WriteFile(filepath.Join(root, "a.txt"), []byte("good\n"), 0o644)).To(gomega.Succeed())

	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}
	plan := planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		Edits: []planartifact.PlanEdit{
			{Path: "a.txt", OldText: "bad", NewText: "good", Purpose: "seed changed path"},
		},
	}

	var (
		stdout             bytes.Buffer
		gotTaskFamily      string
		gotPrompt          string
		gotDisableThinking bool
		gotUseActiveModel  bool
	)

	report, err := runReviewRepairPhaseForTest(
		cfg,
		"Review behavior.",
		plan,
		nil,
		nil,
		planexecute.ExecutorPhaseReport{},
		planexecute.Options{UseActiveModel: true},
		&stdout,
		func(_ config.Config, prompt string, opts cmdruntime.RunOptions, _ io.Writer) (string, error) {
			gotTaskFamily = opts.TaskFamily
			gotPrompt = prompt
			gotDisableThinking = opts.DisableThinking
			gotUseActiveModel = opts.UseActiveModel

			return "Decision: pass\n\nVerification Checklist:\n- Required behavior: good is present.\n- Scope safety: no corrective edit needed.", nil
		},
		nil,
	)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("review-repair pass should be accepted: %v", err))
	gomega.Expect(report.Status).To(gomega.Equal(plancontract.PlanExecuteReviewStatusPassed), fmt.Sprintf("unexpected report: %#v", report))
	gomega.Expect(report.StatusDetail).To(gomega.Equal("no corrective edits"), fmt.Sprintf("unexpected report: %#v", report))
	gomega.Expect(gotTaskFamily).To(gomega.Equal(string(autonomy.TaskFamilyPlanThenExecuteReview)), fmt.Sprintf("review-repair should use review-repair task family, got %q", gotTaskFamily))
	gomega.Expect(gotDisableThinking).To(gomega.BeTrue(), "review-repair must force visible no-thinking output for its terminal contract")
	gomega.Expect(gotUseActiveModel).To(gomega.BeTrue(), "review-repair must preserve plan-execute active model override")

	for _, want := range []string{
		"## Terminal Output Templates",
		"Do not add prose before or after the chosen shape",
		"Pass shape:",
		"Corrective plan shape:",
		"Blocker shape:",
	} {
		gomega.Expect(gotPrompt).To(gomega.ContainSubstring(want), fmt.Sprintf("review-repair prompt missing %q:\n%s", want, gotPrompt))

	}
	gomega.Expect(gotPrompt).NotTo(gomega.ContainSubstring("The plan-submission capability must cover every brief-required edit once"), fmt.Sprintf("review-repair prompt must not include planner-phase submit_plan closeout addendum:\n%s", gotPrompt))

})
var _ = ginkgo.It("run plan execute main resume reruns review repair only", func() {
	configPath := writePlanExecuteConfig()
	root := filepath.Dir(configPath)
	gomega.Expect(os.WriteFile(filepath.Join(root, "a.txt"), []byte("old\n"), 0o644)).To(gomega.Succeed())

	prompt := "Make a.txt say new."

	var firstStdout, firstStderr bytes.Buffer

	plannerSessionID := ""

	code := RunMainWithTelemetry(
		[]string{"-config", configPath, "-prompt", prompt},
		&firstStdout,
		&firstStderr,
		func(cfg config.Config, _ string, opts cmdruntime.RunOptions, _ io.Writer) (string, error) {
			switch opts.TaskFamily {
			case string(autonomy.TaskFamilyPlanThenExecutePlanner):
				plannerSessionID = cfg.Workspace.SessionID
				writePlanExecuteTestArtifact(cfg.Workspace.Root, cfg.Workspace.SessionID, planartifact.PlanArtifact{
					SchemaVersion: planartifact.SchemaVersion,
					Created:       time.Now().UTC().Format(time.RFC3339Nano),
					SessionID:     cfg.Workspace.SessionID,
					Rationale:     "change a.txt",
					Edits: []planartifact.PlanEdit{
						{Path: "a.txt", OldText: "old\n", NewText: "new\n", Purpose: "apply requested text"},
					},
				})

				return "submitted plan", nil
			case string(autonomy.TaskFamilyPlanThenExecuteReview):
				return "Decision: pass\nNo checklist.", nil
			default:
				ginkgo.Fail(fmt.Sprintf("unexpected task family %q", opts.TaskFamily))

				return "", nil
			}
		},
		nil,
	)
	gomega.Expect(code).To(gomega.Equal(1), fmt.Sprintf("first run should fail review checklist gate, got %d stdout=%s stderr=%s", code, firstStdout.String(), firstStderr.String()))

	state, _, found, err := planexecute.TryLoadProgressState(root, plannerSessionID)
	gomega.Expect(err).To(gomega.BeNil(), fmt.Sprintf("load progress state: %v", err))
	gomega.Expect(found).To(gomega.BeTrue(), fmt.Sprintf("load progress state: %v", err))
	gomega.Expect(state.Phase).To(gomega.Equal(plancontract.PlanExecuteProgressPhaseReviewRepair), fmt.Sprintf("expected failed review state, got phase=%s status=%s", state.Phase, state.Status))
	gomega.Expect(state.Status).To(gomega.Equal(plancontract.PlanExecuteProgressStatusFailed), fmt.Sprintf("expected failed review state, got phase=%s status=%s", state.Phase, state.Status))

	var resumedStdout, resumedStderr bytes.Buffer

	reviewCalls := 0
	resumedPrompt := ""

	code = RunMainWithTelemetry(
		[]string{"-config", configPath, "-prompt", prompt, "-resume", plannerSessionID},
		&resumedStdout,
		&resumedStderr,
		func(_ config.Config, prompt string, opts cmdruntime.RunOptions, _ io.Writer) (string, error) {
			gomega.Expect(opts.TaskFamily).To(gomega.Equal(string(autonomy.TaskFamilyPlanThenExecuteReview)), fmt.Sprintf("resume should run review-repair only, got task family %q", opts.TaskFamily))

			reviewCalls++

			resumedPrompt = prompt

			return "Decision: pass\n\nVerification Checklist:\n- Required behavior: a.txt changed from old to new.\n- Scope safety: no corrective edit needed.", nil
		},
		nil,
	)
	gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("resume should pass review, got %d stdout=%s stderr=%s", code, resumedStdout.String(), resumedStderr.String()))
	gomega.Expect(reviewCalls).To(gomega.Equal(1), fmt.Sprintf("resume should call review once, got %d", reviewCalls))
	gomega.Expect(resumedPrompt).To(gomega.ContainSubstring("-old"), fmt.Sprintf("resumed review prompt should restore executor baseline diff, got:\n%s", resumedPrompt))
	gomega.Expect(resumedPrompt).To(gomega.ContainSubstring("+new"), fmt.Sprintf("resumed review prompt should restore executor baseline diff, got:\n%s", resumedPrompt))

	if got := readFileString(filepath.Join(root, "a.txt")); got != "new\n" {
		ginkgo.Fail(fmt.Sprintf("resume should not reapply executor edits, got %q", got))
	}
})
var _ = ginkgo.It("run plan execute main resume uses persisted typed edit set obligations", func() {
	configPath := writePlanExecuteConfig()
	root := filepath.Dir(configPath)
	gomega.Expect(os.WriteFile(filepath.Join(root, "feature_test.go"), []byte("package demo\n\nfunc TestFeature(t *testing.T) {}\n"), 0o644)).To(gomega.Succeed())

	prompt := "Add coverage for existing feature behavior."

	var firstStdout, firstStderr bytes.Buffer

	plannerSessionID := ""

	code := RunMainWithTelemetry(
		[]string{"-config", configPath, "-prompt", prompt},
		&firstStdout,
		&firstStderr,
		func(cfg config.Config, _ string, opts cmdruntime.RunOptions, _ io.Writer) (string, error) {
			switch opts.TaskFamily {
			case string(autonomy.TaskFamilyPlanThenExecutePlanner):
				plannerSessionID = cfg.Workspace.SessionID
				writePlanExecuteTestArtifact(cfg.Workspace.Root, cfg.Workspace.SessionID, planartifact.PlanArtifact{
					SchemaVersion: planartifact.SchemaVersion,
					Created:       time.Now().UTC().Format(time.RFC3339Nano),
					SessionID:     cfg.Workspace.SessionID,
					Rationale:     "test-only coverage plan",
					Edits: []planartifact.PlanEdit{
						{Path: "feature_test.go", OldText: "func TestFeature(t *testing.T) {}\n", NewText: "func TestFeature(t *testing.T) {\n\tt.Helper()\n}\n", Purpose: "add coverage detail"},
					},
				})

				return "submitted test-only plan", nil
			case string(autonomy.TaskFamilyPlanThenExecuteReview):
				return "Decision: pass\nNo checklist.", nil
			default:
				ginkgo.Fail(fmt.Sprintf("unexpected task family %q", opts.TaskFamily))

				return "", nil
			}
		},
		nil,
	)
	gomega.Expect(code).To(gomega.Equal(1), fmt.Sprintf("first run should fail review checklist gate, got %d stdout=%s stderr=%s", code, firstStdout.String(), firstStderr.String()))

	state, _, found, err := planexecute.TryLoadProgressState(root, plannerSessionID)
	gomega.Expect(err).To(gomega.BeNil(), fmt.Sprintf("load progress state: %v", err))
	gomega.Expect(found).To(gomega.BeTrue(), fmt.Sprintf("load progress state: %v", err))

	state.EditSetObligations = plancontract.EditSetObligations{}.With(plancontract.EditSetObligationKindImplementationEdit, "persisted_retry")
	if _, err := planexecute.SaveProgressState(root, &state); err != nil {
		ginkgo.Fail(fmt.Sprintf("save progress state: %v", err))
	}

	var resumedStdout, resumedStderr bytes.Buffer

	resumedPrompt := ""

	code = RunMainWithTelemetry(
		[]string{"-config", configPath, "-prompt", prompt, "-resume", plannerSessionID, "-json"},
		&resumedStdout,
		&resumedStderr,
		func(_ config.Config, prompt string, opts cmdruntime.RunOptions, _ io.Writer) (string, error) {
			gomega.Expect(opts.TaskFamily).To(gomega.Equal(string(autonomy.TaskFamilyPlanThenExecuteReview)), fmt.Sprintf("resume should run review-repair only, got task family %q", opts.TaskFamily))

			resumedPrompt = prompt

			return "Decision: pass\n\nVerification Checklist:\n- Required behavior: test coverage is present.\n- Scope safety: no corrective edit needed.", nil
		},
		nil,
	)
	gomega.Expect(code).To(gomega.Equal(1), fmt.Sprintf("resume should reject pass against persisted implementation obligation, got %d stdout=%s stderr=%s", code, resumedStdout.String(), resumedStderr.String()))
	gomega.Expect(resumedStderr.String()).To(gomega.ContainSubstring(string(plancontract.PlanExecuteFailureMissingRequestedImplementationEdit)), fmt.Sprintf("expected persisted implementation obligation rejection, stderr=%s", resumedStderr.String()))
	gomega.Expect(resumedPrompt).To(gomega.ContainSubstring("requested_implementation_edit_missing"), fmt.Sprintf("review prompt should expose persisted implementation obligation, got:\n%s", resumedPrompt))

})
var _ = ginkgo.It("run plan execute main resume rejects missing session", func() {
	configPath := writePlanExecuteConfig()

	var stdout, stderr bytes.Buffer

	code := RunMainWithTelemetry(
		[]string{"-config", configPath, "-prompt", "resume missing", "-resume", "missing-session"},
		&stdout,
		&stderr,
		func(config.Config, string, cmdruntime.RunOptions, io.Writer) (string, error) {
			ginkgo.Fail("prompt runner should not be called for missing resume state")

			return "", nil
		},
		nil,
	)
	gomega.Expect(code).To(gomega.Equal(1), fmt.Sprintf("expected missing resume to fail, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String()))
	gomega.Expect(stderr.String()).To(gomega.ContainSubstring("does not exist"), fmt.Sprintf("stderr should explain missing resume state, got %s", stderr.String()))

})
var _ = ginkgo.It("run review repair phase degrades when review model unavailable", func() {
	root := ginkgo.GinkgoT().TempDir()
	gomega.Expect(os.WriteFile(filepath.Join(root, "a.txt"), []byte("good\n"), 0o644)).To(gomega.Succeed())

	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}
	plan := planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		Edits: []planartifact.PlanEdit{
			{Path: "a.txt", OldText: "bad", NewText: "good", Purpose: "seed changed path"},
		},
	}

	var stdout bytes.Buffer

	report, err := runReviewRepairPhaseForTest(
		cfg,
		"Review behavior.",
		plan,
		nil,
		nil,
		planexecute.ExecutorPhaseReport{Succeeded: 1, Total: 1},
		planexecute.Options{JSONEvents: true},
		&stdout,
		func(_ config.Config, _ string, _ cmdruntime.RunOptions, _ io.Writer) (string, error) {
			return "", fmt.Errorf("Post \"http://127.0.0.1:47781/v1/chat/completions\": EOF")
		},
		nil,
	)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("review model transport failure should degrade after executor success, got %v", err))
	gomega.Expect(report.Status).To(gomega.Equal(plancontract.PlanExecuteReviewStatusDegraded), fmt.Sprintf("unexpected degraded review report: %#v", report))
	gomega.Expect(report.StatusDetail).To(gomega.Equal("review unavailable"), fmt.Sprintf("unexpected degraded review report: %#v", report))
	gomega.Expect(report.ReasonClass).To(gomega.Equal("backend_eof"), fmt.Sprintf("unexpected degraded review report: %#v", report))

	events := jsoneventstest.DecodeLines(ginkgo.GinkgoT(), stdout.String())
	ev, ok := jsoneventstest.FindByType(events, plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventReviewRepairDegraded))
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("expected review_repair_degraded event, got %s", stdout.String()))
	gomega.Expect(ev["reason_class"]).To(gomega.Equal("backend_eof"), fmt.Sprintf("event should carry typed degraded status and bounded unavailable detail, got %#v", ev))
	gomega.Expect(ev["status"]).To(gomega.Equal("degraded"), fmt.Sprintf("event should carry typed degraded status and bounded unavailable detail, got %#v", ev))
	gomega.Expect(ev["status_detail"]).To(gomega.Equal("review unavailable"), fmt.Sprintf("event should carry typed degraded status and bounded unavailable detail, got %#v", ev))

	if _, ok := jsoneventstest.FindByType(events, plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventReviewRepairFailed)); ok {
		ginkgo.Fail(fmt.Sprintf("transport degradation must not be emitted as failed after executor success: %s", stdout.String()))
	}
})
var _ = ginkgo.It("run review repair phase applies checklist repair plan within scope", func() {
	root := ginkgo.GinkgoT().TempDir()
	gomega.Expect(os.WriteFile(filepath.Join(root, "a.txt"), []byte("bad\n"), 0o644)).To(gomega.Succeed())

	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}
	plan := planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		Edits: []planartifact.PlanEdit{
			{Path: "a.txt", OldText: "old", NewText: "bad", Purpose: "seed changed path"},
		},
	}

	var stdout bytes.Buffer

	report, err := runReviewRepairPhaseForTest(
		cfg,
		"Make a.txt say good.",
		plan,
		nil,
		nil,
		planexecute.ExecutorPhaseReport{},
		planexecute.Options{},
		&stdout,
		func(cfg config.Config, _ string, _ cmdruntime.RunOptions, _ io.Writer) (string, error) {
			writePlanExecuteTestArtifact(cfg.Workspace.Root, cfg.Workspace.SessionID, planartifact.PlanArtifact{
				SchemaVersion: planartifact.SchemaVersion,
				Created:       time.Now().UTC().Format(time.RFC3339Nano),
				SessionID:     cfg.Workspace.SessionID,
				Rationale:     "Verification Checklist:\n- Required behavior: a.txt must say good; current diff says bad.\n",
				Edits: []planartifact.PlanEdit{
					{Path: "a.txt", OldText: "bad", NewText: "good", Purpose: "repair reviewed behavior"},
				},
			})

			return "submitted repair", nil
		},
		nil,
	)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("review-repair should apply scoped repair: %v", err))
	gomega.Expect(report.Status).To(gomega.Equal(plancontract.PlanExecuteReviewStatusRepaired), fmt.Sprintf("unexpected review report: %#v", report))
	gomega.Expect(report.Repairs).To(gomega.Equal(1), fmt.Sprintf("unexpected review report: %#v", report))

	got, err := os.ReadFile(filepath.Join(root, "a.txt"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read a.txt: %v", err))
	gomega.Expect(string(got)).To(gomega.Equal("good\n"), fmt.Sprintf("repair should update a.txt, got %q", string(got)))

})
var _ = ginkgo.It("run review repair phase rejects out of scope repair before writes", func() {
	root := ginkgo.GinkgoT().TempDir()
	gomega.Expect(os.WriteFile(filepath.Join(root, "a.txt"), []byte("bad\n"), 0o644)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(filepath.Join(root, "surprise.txt"), []byte("keep\n"), 0o644)).To(gomega.Succeed())

	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}
	plan := planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		Edits: []planartifact.PlanEdit{
			{Path: "a.txt", OldText: "old", NewText: "bad", Purpose: "seed changed path"},
		},
	}

	var stdout bytes.Buffer

	_, err := runReviewRepairPhaseForTest(
		cfg,
		"Review behavior.",
		plan,
		nil,
		nil,
		planexecute.ExecutorPhaseReport{},
		planexecute.Options{},
		&stdout,
		func(cfg config.Config, _ string, _ cmdruntime.RunOptions, _ io.Writer) (string, error) {
			writePlanExecuteTestArtifact(cfg.Workspace.Root, cfg.Workspace.SessionID, planartifact.PlanArtifact{
				SchemaVersion: planartifact.SchemaVersion,
				Created:       time.Now().UTC().Format(time.RFC3339Nano),
				SessionID:     cfg.Workspace.SessionID,
				Rationale:     "Verification Checklist:\n- Scope safety failed.\n",
				Edits: []planartifact.PlanEdit{
					{Path: "surprise.txt", OldText: "keep", NewText: "mutated", Purpose: "out-of-scope repair"},
				},
			})

			return "submitted repair", nil
		},
		nil,
	)
	gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprintf("expected out-of-scope review repair rejection"))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("allowed_edit_paths"), fmt.Sprintf("expected allowed_edit_paths rejection, got %v", err))

	got, err := os.ReadFile(filepath.Join(root, "surprise.txt"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read surprise.txt: %v", err))
	gomega.Expect(string(got)).To(gomega.Equal("keep\n"), fmt.Sprintf("out-of-scope repair must not write surprise.txt, got %q", string(got)))

})
var _ = ginkgo.It("run review repair phase rejects no op assertion repair before writes", func() {
	root := ginkgo.GinkgoT().TempDir()
	original := "extends Node\n\nfunc test_case():\n\tpass\n"
	gomega.Expect(os.WriteFile(filepath.Join(root, "suite.gd"), []byte(original), 0o644)).To(gomega.Succeed())

	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}
	plan := planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		Edits: []planartifact.PlanEdit{
			{Path: "suite.gd", OldText: "\tpass\n", NewText: "\t# missing behavioral assertion\n\tpass\n", Purpose: "seed changed path"},
		},
	}

	var stdout bytes.Buffer

	_, err := runReviewRepairPhaseForTest(
		cfg,
		"Review behavior.",
		plan,
		nil,
		nil,
		planexecute.ExecutorPhaseReport{},
		planexecute.Options{},
		&stdout,
		func(cfg config.Config, _ string, _ cmdruntime.RunOptions, _ io.Writer) (string, error) {
			writePlanExecuteTestArtifact(cfg.Workspace.Root, cfg.Workspace.SessionID, planartifact.PlanArtifact{
				SchemaVersion: planartifact.SchemaVersion,
				Created:       time.Now().UTC().Format(time.RFC3339Nano),
				SessionID:     cfg.Workspace.SessionID,
				Rationale:     "Verification Checklist:\n- Required behavior still needs a concrete assertion.\n",
				Edits: []planartifact.PlanEdit{
					{Path: "suite.gd", OldText: "\tpass\n", NewText: "\t_assert_true(true, \"contract validated\")\n", Purpose: "fake behavioral proof"},
				},
			})

			return "submitted repair", nil
		},
		nil,
	)
	gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprintf("expected no-op assertion repair rejection"))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("no-op assertion"), fmt.Sprintf("expected no-op assertion rejection, got %v", err))

	got, readErr := os.ReadFile(filepath.Join(root, "suite.gd"))
	gomega.Expect(readErr).To(gomega.BeNil(), fmt.Sprintf("read suite.gd: %v", readErr))
	gomega.Expect(string(got)).To(gomega.Equal(original), fmt.Sprintf("review-repair preflight must reject before writes, got %q", string(got)))

})

func writePlanExecuteTestArtifact(root string, sessionID string, artifact planartifact.PlanArtifact) string {

	dir := filepath.Join(root, ".idiom", "plans")
	gomega.Expect(os.MkdirAll(dir, 0o755)).To(gomega.Succeed())

	if artifact.Created == "" {
		artifact.Created = time.Now().UTC().Format(time.RFC3339Nano)
	}

	if artifact.SchemaVersion == "" {
		artifact.SchemaVersion = planartifact.SchemaVersion
	}

	if artifact.SchemaVersion == planartifact.SchemaVersion {
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
			artifact.Evidence = []planartifact.PlanArtifactEvidence{{Kind: "test_fixture", Summary: "bounded plan-execute test artifact"}}
		}

		if len(artifact.VerificationObligations) == 0 {
			artifact.VerificationObligations = []planartifact.PlanArtifactVerificationObligation{{Kind: "test_lane_sanity", Status: "open", Required: true}}
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
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal plan artifact: %v", err))

	path := filepath.Join(dir, time.Now().UTC().Format("20060102-150405000")+".plan.json")
	gomega.Expect(os.WriteFile(path, body, 0o644)).To(gomega.Succeed())

	return path
}

func planExecuteTestEditPaths(edits []planartifact.PlanEdit) []string {
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

func runReviewRepairPhaseForTest(
	cfg config.Config,
	originalPrompt string,
	plan planartifact.PlanArtifact,
	requiredEditPaths []string,
	baseline *planexecute.ReviewRepairBaseline,
	executorReport planexecute.ExecutorPhaseReport,
	opts planexecute.Options,
	stdout io.Writer,
	promptRun cmdruntime.PromptRunner,
	rec *telemetry.Recorder,
) (planexecute.ReviewRepairPhaseReport, error) {
	return planexecute.RunReviewRepairPhaseWithDependencies(
		cfg,
		originalPrompt,
		plan,
		requiredEditPaths,
		baseline,
		executorReport,
		opts,
		stdout,
		AdaptPromptRunner(promptRun),
		rec,
		planexecuteadapters.Dependencies(),
	)
}

func runExecutorPhaseForTest(cfg config.Config, plan planartifact.PlanArtifact, opts planexecute.ExecutorPhaseOptions) (planexecute.ExecutorPhaseReport, error) {
	return planexecute.RunExecutorPhaseWithDependencies(cfg, plan, opts, planexecuteadapters.Dependencies())
}

var _ = ginkgo.It("run executor phase dispatches edits in order", func() {
	// Seed two files, run the executor phase against a plan that
	// mutates each, and verify both writes landed.
	root := ginkgo.GinkgoT().TempDir()
	gomega.Expect(os.WriteFile(filepath.Join(root, "a.txt"), []byte("alpha\n"), 0o644)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(filepath.Join(root, "b.txt"), []byte("beta\n"), 0o644)).To(gomega.Succeed())

	plan := planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		Rationale:     "rename markers",
		Edits: []planartifact.PlanEdit{
			{Path: "a.txt", OldText: "alpha", NewText: "ALPHA", Purpose: "uppercase a"},
			{Path: "b.txt", OldText: "beta", NewText: "BETA", Purpose: "uppercase b"},
		},
	}
	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}

	var stdout bytes.Buffer

	report, err := runExecutorPhaseForTest(cfg, plan, planexecute.ExecutorPhaseOptions{RequiredEditPaths: nil, AllowedEditPaths: nil, VerificationCommands: nil, JSONEvents: false, Stdout: &stdout})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("executor phase failed: %v", err))
	gomega.Expect(report.Total).To(gomega.Equal(2), fmt.Sprintf("unexpected report: %#v", report))
	gomega.Expect(report.Succeeded).To(gomega.Equal(2), fmt.Sprintf("unexpected report: %#v", report))
	gomega.Expect(report.Failed).To(gomega.Equal(0), fmt.Sprintf("unexpected report: %#v", report))

	got, err := os.ReadFile(filepath.Join(root, "a.txt"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read a.txt: %v", err))
	gomega.Expect(string(got)).To(gomega.ContainSubstring("ALPHA"), fmt.Sprintf("a.txt should contain ALPHA after edit, got %q", string(got)))

	got, err = os.ReadFile(filepath.Join(root, "b.txt"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read b.txt: %v", err))
	gomega.Expect(string(got)).To(gomega.ContainSubstring("BETA"), fmt.Sprintf("b.txt should contain BETA after edit, got %q", string(got)))

})
var _ = ginkgo.It("run executor phase runs source sanity after source edits", func() {
	root := ginkgo.GinkgoT().TempDir()
	gomega.Expect(os.WriteFile(filepath.Join(root, "suite.gd"), []byte("extends Node\n\nfunc helper():\n\tpass\n"), 0o644)).To(gomega.Succeed())

	plan := planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		Edits: []planartifact.PlanEdit{
			{Path: "suite.gd", OldText: "\tpass\n", NewText: "", Purpose: "remove helper body"},
		},
	}
	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}

	var stdout bytes.Buffer

	report, err := runExecutorPhaseForTest(cfg, plan, planexecute.ExecutorPhaseOptions{RequiredEditPaths: nil, AllowedEditPaths: nil, VerificationCommands: nil, JSONEvents: true, Stdout: &stdout})
	gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprintf("expected source_sanity failure after source edit"))
	gomega.Expect(report.Succeeded).To(gomega.Equal(1), fmt.Sprintf("source_sanity runs after successful dispatch; unexpected report %#v", report))
	gomega.Expect(report.Failed).To(gomega.Equal(0), fmt.Sprintf("source_sanity runs after successful dispatch; unexpected report %#v", report))
	gomega.Expect(report.SourceSanity).To(gomega.HaveLen(1), fmt.Sprintf("executor report should retain failed source_sanity evidence, got %#v", report.SourceSanity))
	gomega.Expect(report.SourceSanity[0].Path).To(gomega.Equal("suite.gd"), fmt.Sprintf("executor report should retain failed source_sanity evidence, got %#v", report.SourceSanity))
	gomega.Expect(report.SourceSanity[0].Status).To(gomega.Equal(plancontract.PhaseEvidenceStatusFailed), fmt.Sprintf("executor report should retain failed source_sanity evidence, got %#v", report.SourceSanity))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("source_sanity failed"), fmt.Sprintf("expected source_sanity error, got %v", err))
	gomega.Expect(stdout.String()).To(gomega.ContainSubstring(plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventSourceSanity)), fmt.Sprintf("expected source_sanity event, got %s", stdout.String()))
	gomega.Expect(stdout.String()).To(gomega.ContainSubstring(`"source_sanity_status":"failed"`), fmt.Sprintf("expected failed source_sanity status in executor_complete, got %s", stdout.String()))

})
var _ = ginkgo.It("run executor phase runs inferred touched go package tests", func() {
	root := ginkgo.GinkgoT().TempDir()
	gomega.Expect(os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/inferred\n\ngo 1.22\n"), 0o644)).To(gomega.Succeed())

	pkgDir := filepath.Join(root, "pkg", "feature")
	gomega.Expect(os.MkdirAll(pkgDir, 0o755)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(filepath.Join(pkgDir, "feature.go"), []byte("package feature\n\nfunc Value() int { return 1 }\n"), 0o644)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(filepath.Join(pkgDir, "feature_test.go"), []byte("package feature\n\nimport \"testing\"\n\nfunc TestValue(t *testing.T) {\n\tif Value() != 1 {\n\t\tt.Fatalf(\"unexpected value\")\n\t}\n}\n"), 0o644)).To(gomega.Succeed())

	plan := planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		Edits: []planartifact.PlanEdit{
			{Path: "pkg/feature/feature.go", OldText: "return 1", NewText: "return 2", Purpose: "introduce a failing touched-package test"},
		},
	}
	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}

	var stdout bytes.Buffer

	report, err := runExecutorPhaseForTest(cfg, plan, planexecute.ExecutorPhaseOptions{RequiredEditPaths: nil, AllowedEditPaths: nil, VerificationCommands: nil, JSONEvents: true, Stdout: &stdout})
	gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprintf("expected inferred touched-package verification failure"))
	gomega.Expect(report.Verification).To(gomega.HaveLen(1), fmt.Sprintf("expected failed inferred verification evidence, got %#v", report.Verification))
	gomega.Expect(report.Verification[0].Command).To(gomega.Equal("go test ./pkg/feature"), fmt.Sprintf("expected failed inferred verification evidence, got %#v", report.Verification))
	gomega.Expect(report.Verification[0].Status).To(gomega.Equal(plancontract.PhaseEvidenceStatusFailed), fmt.Sprintf("expected failed inferred verification evidence, got %#v", report.Verification))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("verification failed for \"go test ./pkg/feature\""), fmt.Sprintf("expected explicit verification failure, got %v", err))
	gomega.Expect(stdout.String()).To(gomega.ContainSubstring(`"command":"go test ./pkg/feature"`), fmt.Sprintf("expected verification event for inferred command, got %s", stdout.String()))

})
var _ = ginkgo.It("run executor phase runs inferred nested module package tests", func() {
	root := ginkgo.GinkgoT().TempDir()
	gomega.Expect(os.WriteFile(filepath.Join(root, "go.work"), []byte("go 1.22\n\nuse ./nested\n"), 0o644)).To(gomega.Succeed())

	moduleDir := filepath.Join(root, "nested")
	gomega.Expect(os.MkdirAll(filepath.Join(moduleDir, "pkg", "feature"), 0o755)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(filepath.Join(moduleDir, "go.mod"), []byte("module example.com/nested\n\ngo 1.22\n"), 0o644)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(filepath.Join(moduleDir, "pkg", "feature", "feature.go"), []byte("package feature\n\nfunc Value() int { return 1 }\n"), 0o644)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(filepath.Join(moduleDir, "pkg", "feature", "feature_test.go"), []byte("package feature\n\nimport \"testing\"\n\nfunc TestValue(t *testing.T) {\n\tif Value() != 1 {\n\t\tt.Fatalf(\"nested module assertion ran: got %d\", Value())\n\t}\n}\n"), 0o644)).To(gomega.Succeed())

	plan := planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		Edits: []planartifact.PlanEdit{
			{Path: "nested/pkg/feature/feature.go", OldText: "return 1", NewText: "return 2", Purpose: "introduce a failing nested touched-package test"},
		},
	}
	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}

	var stdout bytes.Buffer

	report, err := runExecutorPhaseForTest(cfg, plan, planexecute.ExecutorPhaseOptions{RequiredEditPaths: nil, AllowedEditPaths: nil, VerificationCommands: nil, JSONEvents: true, Stdout: &stdout})
	gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprintf("expected inferred nested-module verification failure"))
	gomega.Expect(report.Verification).To(gomega.HaveLen(1), fmt.Sprintf("expected one inferred verification check, got %#v report=%#v err=%v stdout=%s", report.Verification, report, err, stdout.String()))

	check := report.Verification[0]
	gomega.Expect(check.Command).To(gomega.Equal("go test ./pkg/feature"), fmt.Sprintf("expected failed nested-module verification evidence, got %#v", check))
	gomega.Expect(check.WorkingDir).To(gomega.Equal("nested"), fmt.Sprintf("expected failed nested-module verification evidence, got %#v", check))
	gomega.Expect(check.Status).To(gomega.Equal(plancontract.PhaseEvidenceStatusFailed), fmt.Sprintf("expected failed nested-module verification evidence, got %#v", check))
	gomega.Expect(check.Error).To(gomega.ContainSubstring("nested module assertion ran"), fmt.Sprintf("expected nested module test output, got %#v", check))
	gomega.Expect(stdout.String()).To(gomega.ContainSubstring(`"working_dir":"nested"`), fmt.Sprintf("expected verification event to include working_dir, got %s", stdout.String()))

})
var _ = ginkgo.It("run executor phase aborts at preflight on missing file", func

// Pre-execute plan validator catches edit 2's missing file before
// any edit dispatches. This is by-design atomic-preflight: rather
// than landing edit 1 then failing edit 2 (the old half-applied
// state), the whole plan is rejected with a single error pointing
// at the bad edit. Counts remain at 0 because no dispatch ran.
() {

	root := ginkgo.GinkgoT().TempDir()
	gomega.Expect(os.WriteFile(filepath.Join(root, "a.txt"), []byte("alpha\n"), 0o644)).To(gomega.Succeed())

	plan := planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		Edits: []planartifact.PlanEdit{
			{Path: "a.txt", OldText: "alpha", NewText: "ALPHA"},
			{Path: "missing.txt", OldText: "x", NewText: "y"},
		},
	}
	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}

	var stdout bytes.Buffer

	report, err := runExecutorPhaseForTest(cfg, plan, planexecute.ExecutorPhaseOptions{RequiredEditPaths: nil, AllowedEditPaths: nil, VerificationCommands: nil, JSONEvents: false, Stdout: &stdout})
	gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprintf("expected preflight abort on missing file, got nil"))
	gomega.Expect(report.Succeeded).To(gomega.Equal(0), fmt.Sprintf("preflight aborts before any dispatch — report should be untouched, got %#v", report))
	gomega.Expect(report.Failed).To(gomega.Equal(0), fmt.Sprintf("preflight aborts before any dispatch — report should be untouched, got %#v", report))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("missing.txt"), fmt.Sprintf("error should name the failing path, got %v", err))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("preflight"), fmt.Sprintf("error should identify the abort layer as preflight, got %v", err))

	// a.txt must NOT have been modified — atomic preflight means
	// nothing landed when one edit was bad.
	got, _ := os.ReadFile(filepath.Join(root, "a.txt"))
	gomega.Expect(string(got)).To(gomega.Equal("alpha\n"), fmt.Sprintf("preflight abort must leave files untouched, got %q", string(got)))

})
var _ = ginkgo.It("run executor phase rejects plan missing required edit path before writes", func() {
	root := ginkgo.GinkgoT().TempDir()
	gomega.Expect(os.WriteFile(filepath.Join(root, "a.txt"), []byte("alpha\n"), 0o644)).To(gomega.Succeed())

	plan := planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		Edits: []planartifact.PlanEdit{
			{Path: "a.txt", OldText: "alpha", NewText: "ALPHA"},
		},
	}
	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}

	var stdout bytes.Buffer

	report, err := runExecutorPhaseForTest(cfg, plan, planexecute.ExecutorPhaseOptions{
		RequiredEditPaths: []string{"a.txt", "b.txt"},
		JSONEvents:        true,
		Stdout:            &stdout,
	})
	gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprintf("expected required_edit_paths preflight failure"))
	gomega.Expect(report.Succeeded).To(gomega.Equal(0), fmt.Sprintf("required path preflight must abort before dispatch, got %#v", report))
	gomega.Expect(report.Failed).To(gomega.Equal(0), fmt.Sprintf("required path preflight must abort before dispatch, got %#v", report))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("b.txt"), fmt.Sprintf("error should name the missing required path, got %v", err))
	gomega.Expect(stdout.String()).To(gomega.ContainSubstring(plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventPreflightFailed)), fmt.Sprintf("expected preflight_failed event, got %s", stdout.String()))

	got, _ := os.ReadFile(filepath.Join(root, "a.txt"))
	gomega.Expect(string(got)).To(gomega.Equal("alpha\n"), fmt.Sprintf("preflight abort must leave files untouched, got %q", string(got)))

})
var _ = ginkgo.It("run executor phase rejects plan outside required edit paths before writes", func() {
	root := ginkgo.GinkgoT().TempDir()
	gomega.Expect(os.WriteFile(filepath.Join(root, "a.txt"), []byte("alpha\n"), 0o644)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(filepath.Join(root, "surprise.txt"), []byte("surprise\n"), 0o644)).To(gomega.Succeed())

	plan := planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		Edits: []planartifact.PlanEdit{
			{Path: "a.txt", OldText: "alpha", NewText: "ALPHA"},
			{Path: "surprise.txt", OldText: "surprise", NewText: "SURPRISE"},
		},
	}
	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}

	var stdout bytes.Buffer

	report, err := runExecutorPhaseForTest(cfg, plan, planexecute.ExecutorPhaseOptions{RequiredEditPaths: []string{"a.txt"}, AllowedEditPaths: nil, VerificationCommands: nil, JSONEvents: true, Stdout: &stdout})
	gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprintf("expected outside required_edit_paths preflight failure"))
	gomega.Expect(report.Succeeded).To(gomega.Equal(0), fmt.Sprintf("scope preflight must abort before dispatch, got %#v", report))
	gomega.Expect(report.Failed).To(gomega.Equal(0), fmt.Sprintf("scope preflight must abort before dispatch, got %#v", report))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("surprise.txt"), fmt.Sprintf("error should name the outside path, got %v", err))

	got, _ := os.ReadFile(filepath.Join(root, "surprise.txt"))
	gomega.Expect(string(got)).To(gomega.Equal("surprise\n"), fmt.Sprintf("preflight abort must leave outside file untouched, got %q", string(got)))

})
var _ = ginkgo.It("preflight plan edits catches anchor mismatch", func() {
	// The donkey-atom failure mode: plan's old_text spans content
	// that doesn't appear in the file in that exact form. Preflight
	// must catch it and name the edit's index + path.
	root := ginkgo.GinkgoT().TempDir()
	gomega.Expect(os.WriteFile(filepath.Join(root, "x.txt"), []byte("line one\nline two\nline three\n"), 0o644)).To(gomega.Succeed())

	plan := planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		Edits: []planartifact.PlanEdit{
			{Path: "x.txt", OldText: "line one\nline three", NewText: "X"},
		},
	}
	got := planexecute.PreflightPlanEditsWithPolicy(root, plan, planexecuteadapters.Dependencies().EditPreflight)
	gomega.Expect(got).NotTo(gomega.BeNil(), fmt.Sprintf("preflight must catch anchor mismatch (combined non-adjacent lines)"))
	gomega.Expect(got.Index).To(gomega.Equal(0), fmt.Sprintf("mismatch should point at edit 0, got index %d", got.Index))
	gomega.Expect(got.Path).To(gomega.Equal("x.txt"), fmt.Sprintf("mismatch should name the path, got %q", got.Path))
	gomega.Expect(strings.ToLower(got.Reason)).To(gomega.ContainSubstring("old_text"), fmt.Sprintf("mismatch reason should mention old_text, got %q", got.Reason))

})
var _ = ginkgo.It("preflight plan edits accepts valid plan", func() {
	root := ginkgo.GinkgoT().TempDir()
	gomega.Expect(os.WriteFile(filepath.Join(root, "x.txt"), []byte("hello\nworld\n"), 0o644)).To(gomega.Succeed())

	plan := planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		Edits: []planartifact.PlanEdit{
			{Path: "x.txt", OldText: "hello", NewText: "HELLO"},
		},
	}
	gomega.Expect(planexecute.PreflightPlanEditsWithPolicy(root, plan, planexecuteadapters.Dependencies().EditPreflight)).To(gomega.BeNil())

})
var _ = ginkgo.It("preflight plan edits rejects stale anchor provenance", func() {
	root := ginkgo.GinkgoT().TempDir()
	gomega.Expect(os.WriteFile(filepath.Join(root, "x.txt"), []byte("hello\nworld\n"), 0o644)).To(gomega.Succeed())

	edits, err := workspace.AttachPlanEditAnchorProvenance(root, []planartifact.PlanEdit{
		{Path: "x.txt", OldText: "hello", NewText: "HELLO"},
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("attach anchor provenance: %v", err))
	gomega.Expect(os.WriteFile(filepath.Join(root, "x.txt"), []byte("prefix\nhello\nworld\n"), 0o644)).To(gomega.Succeed())

	plan := planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		Edits:         edits,
	}
	got := planexecute.PreflightPlanEditsWithPolicy(root, plan, planexecuteadapters.Dependencies().EditPreflight)
	gomega.Expect(got).NotTo(gomega.BeNil(), fmt.Sprintf("preflight must reject stale anchor provenance before writes"))
	gomega.Expect(got.Index).To(gomega.Equal(0), fmt.Sprintf("mismatch should point at edit 0 in x.txt, got %#v", got))
	gomega.Expect(got.Path).To(gomega.Equal("x.txt"), fmt.Sprintf("mismatch should point at edit 0 in x.txt, got %#v", got))
	gomega.Expect(got.Reason).To(gomega.ContainSubstring("anchor provenance stale"), fmt.Sprintf("mismatch should report stale provenance, got %q", got.Reason))
	gomega.Expect(got.Reason).To(gomega.ContainSubstring("file fingerprint changed"), fmt.Sprintf("mismatch should report stale provenance, got %q", got.Reason))

})
var _ = ginkgo.It("preflight plan edits rejects escaping path before read", func() {
	root := ginkgo.GinkgoT().TempDir()
	outside := filepath.Join(filepath.Dir(root), "outside.txt")
	gomega.Expect(os.WriteFile(outside, []byte("secret\n"), 0o644)).To(gomega.Succeed())

	plan := planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		Edits: []planartifact.PlanEdit{
			{Path: "../outside.txt", OldText: "secret", NewText: "redacted"},
		},
	}

	got := planexecute.PreflightPlanEditsWithPolicy(root, plan, planexecuteadapters.Dependencies().EditPreflight)
	gomega.Expect(got).NotTo(gomega.BeNil(), fmt.Sprintf("expected invalid path preflight mismatch, got %#v", got))
	gomega.Expect(got.Reason).To(gomega.Equal("edit has invalid path"), fmt.Sprintf("expected invalid path preflight mismatch, got %#v", got))

})
var _ = ginkgo.It("preflight plan edits rejects ambiguous old text before writes", func() {
	root := ginkgo.GinkgoT().TempDir()
	gomega.Expect(os.WriteFile(filepath.Join(root, "x.txt"), []byte("set_floor(30.0f);\nother();\nset_floor(30.0f);\n"), 0o644)).To(gomega.Succeed())

	plan := planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		Edits: []planartifact.PlanEdit{
			{Path: "x.txt", OldText: "set_floor(30.0f);", NewText: "floor = 30.0f;"},
		},
	}
	got := planexecute.PreflightPlanEditsWithPolicy(root, plan, planexecuteadapters.Dependencies().EditPreflight)
	gomega.Expect(got).NotTo(gomega.BeNil(), fmt.Sprintf("preflight must catch ambiguous old_text before executor dispatch"))
	gomega.Expect(got.Index).To(gomega.Equal(0), fmt.Sprintf("mismatch should point at edit 0 in x.txt, got %#v", got))
	gomega.Expect(got.Path).To(gomega.Equal("x.txt"), fmt.Sprintf("mismatch should point at edit 0 in x.txt, got %#v", got))
	gomega.Expect(got.Reason).To(gomega.ContainSubstring("matched 2 locations"), fmt.Sprintf("mismatch reason should report duplicate matches, got %q", got.Reason))

})
var _ = ginkgo.It("preflight plan edits allows replace all repeated old text", func() {
	root := ginkgo.GinkgoT().TempDir()
	gomega.Expect(os.WriteFile(filepath.Join(root, "x.txt"), []byte("set_floor(30.0f);\nset_floor(30.0f);\n"), 0o644)).To(gomega.Succeed())

	plan := planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		Edits: []planartifact.PlanEdit{
			{Path: "x.txt", OldText: "set_floor(30.0f);", NewText: "floor = 30.0f;", ReplaceAll: true},
		},
	}
	gomega.Expect(planexecute.PreflightPlanEditsWithPolicy(root, plan, planexecuteadapters.Dependencies().EditPreflight)).To(gomega.BeNil())

})
var _ = ginkgo.It("preflight plan unsupported proof claims rejects no op assertion", func() {
	plan := planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		Edits: []planartifact.PlanEdit{
			{
				Path:    "tests/panel_contract.gd",
				NewText: "func test_contract():\n\t_assert_true(true, \"contract validated\")\n",
			},
		},
	}
	got := planexecute.PreflightPlanUnsupportedProofClaimsWithPolicy(plan, planexecuteadapters.Dependencies().EditPreflight)
	gomega.Expect(got).NotTo(gomega.BeNil(), fmt.Sprintf("expected unsupported proof preflight to reject no-op assertion"))
	gomega.Expect(got.Path).To(gomega.Equal("tests/panel_contract.gd"), fmt.Sprintf("unexpected mismatch: %#v", got))
	gomega.Expect(got.Reason).To(gomega.ContainSubstring("no-op assertion"), fmt.Sprintf("unexpected mismatch: %#v", got))

})
var _ = ginkgo.It("preflight plan unsupported proof claims rejects self disqualifying proof comment", func() {
	plan := planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		Edits: []planartifact.PlanEdit{
			{
				Path:    "tests/panel_contract.gd",
				NewText: "func test_contract():\n\t# simplified check: for now, we just verify that it exists and can be called\n\tpass\n",
			},
		},
	}
	got := planexecute.PreflightPlanUnsupportedProofClaimsWithPolicy(plan, planexecuteadapters.Dependencies().EditPreflight)
	gomega.Expect(got).NotTo(gomega.BeNil(), fmt.Sprintf("expected unsupported proof preflight to reject self-disqualifying proof comment"))
	gomega.Expect(got.Path).To(gomega.Equal("tests/panel_contract.gd"), fmt.Sprintf("unexpected mismatch: %#v", got))
	gomega.Expect(got.Reason).To(gomega.ContainSubstring("self-disqualifying"), fmt.Sprintf("unexpected mismatch: %#v", got))

})
var _ = ginkgo.It("preflight plan unsupported proof claims ignores prose files", func() {
	plan := planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		Edits: []planartifact.PlanEdit{
			{
				Path:    "docs/review.md",
				NewText: "This retrospective says we cannot easily test the old behavior.\n",
			},
		},
	}
	gomega.Expect(planexecute.PreflightPlanUnsupportedProofClaimsWithPolicy(plan, planexecuteadapters.Dependencies().EditPreflight)).To(gomega.BeNil())

})
var _ = ginkgo.It("preflight plan edits skips range shaped edits", func(

// Range-shaped edits (start_line+end_line+content, no old_text)
// don't depend on substring matching; preflight must skip them
// and let the executor handle their validation.
) {

	root := ginkgo.GinkgoT().TempDir()
	gomega.Expect(os.WriteFile(filepath.Join(root, "x.txt"), []byte("a\nb\nc\n"), 0o644)).To(gomega.Succeed())

	plan := planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		Edits: []planartifact.PlanEdit{
			{Path: "x.txt", StartLine: 1, EndLine: 1, Content: "A\n"},
		},
	}
	gomega.Expect(planexecute.PreflightPlanEditsWithPolicy(root, plan, planexecuteadapters.Dependencies().EditPreflight)).To(gomega.BeNil())

})
var _ = ginkgo.It("preflight plan edits accepts insert after line shape", func() {
	root := ginkgo.GinkgoT().TempDir()
	gomega.Expect(os.WriteFile(filepath.Join(root, "x.txt"), []byte("a\nb\n"), 0o644)).To(gomega.Succeed())

	plan := planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		Edits: []planartifact.PlanEdit{
			{Path: "x.txt", InsertAfterLine: 1, Content: "inserted"},
		},
	}
	gomega.Expect(planexecute.PreflightPlanEditsWithPolicy(root, plan, planexecuteadapters.Dependencies().EditPreflight)).To(gomega.BeNil())

})
var _ = ginkgo.It("run executor phase applies insert after line shape", func() {
	root := ginkgo.GinkgoT().TempDir()
	gomega.Expect(os.WriteFile(filepath.Join(root, "x.txt"), []byte("a\nb"), 0o644)).To(gomega.Succeed())

	plan := planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		Edits: []planartifact.PlanEdit{
			{Path: "x.txt", InsertAfterLine: 2, Content: "c\n", Purpose: "Append c."},
		},
	}
	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}

	var stdout bytes.Buffer

	report, err := runExecutorPhaseForTest(cfg, plan, planexecute.ExecutorPhaseOptions{RequiredEditPaths: []string{"x.txt"}, AllowedEditPaths: nil, VerificationCommands: nil, JSONEvents: true, Stdout: &stdout})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("executor phase failed: %v\n%s", err, stdout.String()))
	gomega.Expect(report.Succeeded).To(gomega.Equal(1), fmt.Sprintf("unexpected report: %#v", report))
	gomega.Expect(report.Failed).To(gomega.Equal(0), fmt.Sprintf("unexpected report: %#v", report))

	got, err := os.ReadFile(filepath.Join(root, "x.txt"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read x.txt: %v", err))
	gomega.Expect(string(got)).To(gomega.Equal("a\nb\nc\n"), fmt.Sprintf("insert_after_line executor result mismatch: %q", string(got)))

})
var _ = ginkgo.It("preflight plan edits rejects adjacent duplicate code line", func() {
	root := ginkgo.GinkgoT().TempDir()
	gomega.Expect(os.WriteFile(filepath.Join(root, "sim.cpp"), []byte("void tick() {\n    auto& c = entities_->colonists.data()[i];\n    c.cultural_work_mult = 1.0f;\n}\n"), 0o644)).To(gomega.Succeed())

	plan := planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		Edits: []planartifact.PlanEdit{
			{
				Path:    "sim.cpp",
				OldText: "    c.cultural_work_mult = 1.0f;\n",
				NewText: "    auto& c = entities_->colonists.data()[i];\n    c.cultural_work_mult = 1.0f;\n",
			},
		},
	}
	got := planexecute.PreflightPlanEditsWithPolicy(root, plan, planexecuteadapters.Dependencies().EditPreflight)
	gomega.Expect(got).NotTo(gomega.BeNil(), fmt.Sprintf("preflight must reject plans that introduce adjacent duplicate code"))
	gomega.Expect(got.Index).To(gomega.Equal(0), fmt.Sprintf("mismatch should point at edit 0 in sim.cpp, got %#v", got))
	gomega.Expect(got.Path).To(gomega.Equal("sim.cpp"), fmt.Sprintf("mismatch should point at edit 0 in sim.cpp, got %#v", got))
	gomega.Expect(got.Reason).To(gomega.ContainSubstring("adjacent duplicate code line"), fmt.Sprintf("mismatch reason should name adjacent duplicate code, got %q", got.Reason))

})
var _ = ginkgo.It("run executor phase rejects completed artifact overwrite before writes", func() {
	root := ginkgo.GinkgoT().TempDir()
	gomega.Expect(os.WriteFile(filepath.Join(root, "a.txt"), []byte("before\n"), 0o644)).To(gomega.Succeed())

	specPath := filepath.Join(root, ".planning", "atoms", "done.md")
	gomega.Expect(os.MkdirAll(filepath.Dir(specPath), 0o755)).To(gomega.Succeed())

	originalSpec := "# Done\n\nStatus: complete\n\nAccepted spec.\n"
	gomega.Expect(os.WriteFile(specPath, []byte(originalSpec), 0o644)).To(gomega.Succeed())

	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}
	plan := planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		Edits: []planartifact.PlanEdit{
			{Path: "a.txt", OldText: "before", NewText: "after"},
			{Path: ".planning/atoms/done.md", NewText: "# Done\n\nNew draft.\n", ReplaceAll: true},
		},
	}

	var stdout bytes.Buffer

	report, err := runExecutorPhaseForTest(cfg, plan, planexecute.ExecutorPhaseOptions{RequiredEditPaths: nil, AllowedEditPaths: nil, VerificationCommands: nil, JSONEvents: false, Stdout: &stdout})
	gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprintf("expected completed artifact overwrite preflight failure"))
	gomega.Expect(report.Succeeded).To(gomega.Equal(0), fmt.Sprintf("preflight failure should happen before dispatch, got %#v", report))
	gomega.Expect(report.Failed).To(gomega.Equal(0), fmt.Sprintf("preflight failure should happen before dispatch, got %#v", report))

	if got, _ := os.ReadFile(filepath.Join(root, "a.txt")); string(got) != "before\n" {
		ginkgo.Fail(fmt.Sprintf("preflight abort must leave earlier files untouched, got %q", string(got)))
	}

	if got, _ := os.ReadFile(specPath); string(got) != originalSpec {
		ginkgo.Fail(fmt.Sprintf("preflight abort must leave completed spec untouched, got %q", string(got)))
	}
})
var _ = ginkgo.It("run executor phase rejects empty plan", func() {
	cfg := config.Config{Workspace: config.Workspace{Root: ginkgo.GinkgoT().TempDir()}}

	var stdout bytes.Buffer

	_, err := runExecutorPhaseForTest(cfg, planartifact.PlanArtifact{SchemaVersion: planartifact.SchemaVersion}, planexecute.ExecutorPhaseOptions{RequiredEditPaths: nil, AllowedEditPaths: nil, VerificationCommands: nil, JSONEvents: false, Stdout: &stdout})
	gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprintf("empty plan should be rejected"))

})
var _ = ginkgo.It("find latest plan artifact returns most recent", func() {
	root := ginkgo.GinkgoT().TempDir()
	dir := filepath.Join(root, ".idiom", "plans")
	gomega.Expect(os.MkdirAll(dir, 0o755)).To(gomega.Succeed())

	// Write two stamped plans + the rolling pointer.
	old := filepath.Join(dir, "20260101-000000000.plan.json")
	newer := filepath.Join(dir, "20260101-000001000.plan.json")
	pointer := filepath.Join(dir, "last.plan.json")

	for _, p := range []string{old, newer, pointer} {
		gomega.Expect(os.WriteFile(p, []byte("{}"), 0o644)).To(gomega.Succeed())

	}
	gomega.

		// Force the newer one to have a later mtime regardless of write
		// order.
		Expect(os.Chtimes(newer, mustTime("2026-04-01T00:00:00Z"), mustTime("2026-04-01T00:00:00Z"))).To(gomega.Succeed())
	gomega.Expect(os.Chtimes(old, mustTime("2026-01-01T00:00:00Z"), mustTime("2026-01-01T00:00:00Z"))).To(gomega.Succeed())

	got, err := planexecuteadapters.FindLatestPlanArtifact(root, time.Time{}, "")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("findLatestPlanArtifact: %v", err))
	gomega.Expect(filepath.Base(got)).To(gomega.Equal(filepath.Base(newer)), fmt.Sprintf("expected newer artifact, got %s", got))

})
var _ = ginkgo.It("find latest plan artifact errors when none exist", func() {
	root := ginkgo.GinkgoT().TempDir()
	if _, err := planexecuteadapters.FindLatestPlanArtifact(root, time.Time{}, ""); err == nil {
		ginkgo.Fail(fmt.Sprintf("expected error when plans dir missing"))
	}
})
var _ = ginkgo.It("find latest plan artifact filters by session id", func

// The headline concurrency-isolation invariant: when a session ID
// is supplied, plans from other sessions (even newer ones) must be
// invisible. Without this filter, two concurrent plan-execute runs
// in the same workspace can load each other's plans and dispatch
// the wrong edits.
() {

	root := ginkgo.GinkgoT().TempDir()
	dir := filepath.Join(root, ".idiom", "plans")
	gomega.Expect(os.MkdirAll(dir, 0o755)).To(gomega.Succeed())

	mine := filepath.Join(dir, "20260510-180000000.plan.json")
	otherNewer := filepath.Join(dir, "20260510-180100000.plan.json")
	gomega.Expect(os.WriteFile(mine, []byte(`{"schema_version":"idiom.plan.v1","session_id":"sid-mine","edits":[{"path":"a","old_text":"x","new_text":"y"}]}`), 0o644)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(otherNewer, []byte(`{"schema_version":"idiom.plan.v1","session_id":"sid-other","edits":[{"path":"a","old_text":"x","new_text":"y"}]}`), 0o644)).To(gomega.Succeed())
	gomega.Expect(os.Chtimes(mine, mustTime("2026-05-10T18:00:00Z"), mustTime("2026-05-10T18:00:00Z"))).To(gomega.Succeed())
	gomega.Expect(os.Chtimes(otherNewer, mustTime("2026-05-10T18:01:00Z"), mustTime("2026-05-10T18:01:00Z"))).To(gomega.Succeed())

	// With sessionID filter: mine is selected even though other is newer.
	got, err := planexecuteadapters.FindLatestPlanArtifact(root, time.Time{}, "sid-mine")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("findLatestPlanArtifact: %v", err))
	gomega.Expect(filepath.Base(got)).To(gomega.Equal(filepath.Base(mine)), fmt.Sprintf("expected my session's plan even though other is newer, got %s", got))

	// Without sessionID (legacy callers): newest wins regardless of attribution.
	got, err = planexecuteadapters.FindLatestPlanArtifact(root, time.Time{}, "")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("findLatestPlanArtifact (no session): %v", err))
	gomega.Expect(filepath.Base(got)).To(gomega.Equal(filepath.Base(otherNewer)), fmt.Sprintf("legacy lookup should return newest, got %s", got))

	// Filter for an unknown session: error, even though other plans exist.
	if _, err := planexecuteadapters.FindLatestPlanArtifact(root, time.Time{}, "sid-nobody"); err == nil {
		ginkgo.Fail(fmt.Sprintf("expected error when no plan matches the requested session"))
	}
})
var _ = ginkgo.It("find latest plan artifact skips artifacts before not before", func() {
	root := ginkgo.GinkgoT().TempDir()
	dir := filepath.Join(root, ".idiom", "plans")
	gomega.Expect(os.MkdirAll(dir, 0o755)).To(gomega.Succeed())

	stale := filepath.Join(dir, "20260101-000000000.plan.json")
	gomega.Expect(os.WriteFile(stale, []byte("{}"), 0o644)).To(gomega.Succeed())
	gomega.Expect(os.Chtimes(stale, mustTime("2026-01-01T00:00:00Z"), mustTime("2026-01-01T00:00:00Z"))).To(gomega.Succeed())

	notBefore := mustTime("2026-04-01T00:00:00Z")
	if _, err := planexecuteadapters.FindLatestPlanArtifact(root, notBefore, ""); err == nil {
		ginkgo.Fail(fmt.Sprintf("expected error when only stale artifacts exist before notBefore"))
	}
})
