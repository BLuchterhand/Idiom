package lane

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"idiom/internal/capabilities/workspace"
	atomintakecontract "idiom/internal/contracts/atomintake"
	lanecontract "idiom/internal/contracts/lane"
	"idiom/internal/contracts/workflowstate"
	"idiom/internal/contracts/writerroute"
	"idiom/internal/evidence/telemetry"
	"idiom/internal/platform/config"
	"idiom/internal/platform/jsonevents/jsoneventstest"
	"idiom/internal/runtime/agent"
	"idiom/internal/runtime/readiness"
	cmdruntime "idiom/internal/surfaces/cli/runtime"
	"idiom/internal/workflows/atomintake"
	"idiom/internal/workflows/brieflint"
	"idiom/internal/workflows/planexecute"
	planexecuterunner "idiom/internal/workflows/planexecute/runner"
	canonicalprompts "idiom/prompts"
)

type laneFakeRunner struct {
	calledMode        string
	calledArgs        []string
	calledPlanOptions planexecute.Options
	calledRunOptions  planexecuterunner.Options
	calledPrompt      string
	exitCode          int
}

var _ = ginkgo.Describe("lane", func() {
	ginkgo.It("lane brief intake projection requires typed dispatchable scope for legacy headings", func() {
		body := "# Atom\n\n## Required Edits\n\n- Change the banner text.\n"
		projection := laneBriefIntakeProjection(body)
		gomega.Expect(projection.NeedsAtomIntake).To(gomega.BeTrue(), fmt.Sprintf("heading-only atom brief should trigger non-dispatchable typed atom intake without scope: %#v", projection))
		gomega.Expect(projection.Dispatchable).To(gomega.BeFalse(), fmt.Sprintf("heading-only atom brief should trigger non-dispatchable typed atom intake without scope: %#v", projection))
		gomega.Expect(projection.State).To(gomega.Equal(workflowstate.StateTerminal), fmt.Sprintf("heading-only atom brief should trigger non-dispatchable typed atom intake without scope: %#v", projection))
		gomega.Expect(projection.Reason).To(gomega.Equal("missing_scope"), fmt.Sprintf("heading-only atom brief should trigger non-dispatchable typed atom intake without scope: %#v", projection))

	})
	ginkgo.It("lane brief intake projection allows legacy headings after scope", func() {
		body := "# Atom\n\n## Files To Modify\n\n- pkg/feature.go\n\n## Required Edits\n\n- Change the banner text.\n"
		projection := laneBriefIntakeProjection(body)
		gomega.Expect(projection.NeedsAtomIntake).To(gomega.BeTrue(), fmt.Sprintf("scoped legacy heading brief should trigger atom intake through compatibility fallback: %#v", projection))
		gomega.Expect(projection.Dispatchable).To(gomega.BeTrue(), fmt.Sprintf("scoped legacy heading brief should trigger atom intake through compatibility fallback: %#v", projection))
		gomega.Expect(projection.LegacyHeadingFallback).To(gomega.BeTrue(), fmt.Sprintf("scoped legacy heading brief should trigger atom intake through compatibility fallback: %#v", projection))
		gomega.Expect(projection.Reason).To(gomega.HavePrefix("legacy_heading:"), fmt.Sprintf("scoped legacy heading brief should trigger atom intake through compatibility fallback: %#v", projection))

	})
	ginkgo.DescribeTable("projects final non-dispatchable atom intake states",
		func(state atomintakecontract.IntakeState, wantTerminal workflowstate.TerminalState) {
			report := atomintake.IntakeReport{State: state, Dispatchable: false}
			preflight := laneBriefIntakeProjectionResult{
				WorkflowID:   workflowstate.WorkflowAtomIntake,
				State:        workflowstate.StateDispatchable,
				Dispatchable: true,
				Reason:       "preflight_marker",
			}

			fields := laneAtomIntakeReportFields(report, preflight)
			gomega.Expect(fields).To(gomega.SatisfyAll(
				gomega.HaveKeyWithValue("workflow_state", string(workflowstate.StateTerminal)),
				gomega.HaveKeyWithValue("workflow_terminal_state", string(wantTerminal)),
				gomega.HaveKeyWithValue("workflow_dispatchable", false),
			))
		},
		ginkgo.Entry("source satisfied", atomintakecontract.StateSourceSatisfied, workflowstate.TerminalStateSourceSatisfied),
		ginkgo.Entry("stale needs rebase", atomintakecontract.StateStaleNeedsRebase, workflowstate.TerminalStateStaleNeedsRebase),
		ginkgo.Entry("invalid scope", atomintakecontract.StateInvalidScope, workflowstate.TerminalStateInvalidScope),
		ginkgo.Entry("blocked dirty files", atomintakecontract.StateBlockedDirty, workflowstate.TerminalStateBlockedDirtyFiles),
		ginkgo.Entry("failed refine draft", atomintakecontract.StateFailedRefine, workflowstate.TerminalStateFailedRefineDraft),
	)
	ginkgo.It("run lane main requires brief", func() {

		var stdout, stderr bytes.Buffer

		fake := &laneFakeRunner{}
		code := runLaneMainTest([]string{"plan-execute"}, &stdout, &stderr, fake)
		gomega.Expect(code).NotTo(gomega.Equal(0), fmt.Sprintf("expected non-zero exit for missing brief, got 0"))
		gomega.Expect(stderr.String()).To(gomega.ContainSubstring("brief"), fmt.Sprintf("error should mention the missing brief, got: %s", stderr.String()))

	})
	ginkgo.It("run lane main rejects unknown mode", func() {
		briefPath := writeBrief("# Test\nDo a thing.\n")

		var stdout, stderr bytes.Buffer

		fake := &laneFakeRunner{}
		code := runLaneMainTest([]string{"bogus-mode", briefPath}, &stdout, &stderr, fake)
		gomega.Expect(code).To(gomega.Equal(2), fmt.Sprintf("expected exit 2 for unknown mode, got %d", code))

		out := stderr.String()
		gomega.Expect(out).To(gomega.ContainSubstring("plan-execute"), fmt.Sprintf("error should list valid modes, got: %s", out))
		gomega.Expect(out).To(gomega.ContainSubstring("implement"), fmt.Sprintf("error should list valid modes, got: %s", out))

	})
	ginkgo.It("run lane main plan execute forwards", func() {
		briefPath := writeBrief("# Plan-execute brief\nFiles To Modify: x.txt")

		var stdout, stderr bytes.Buffer

		fake := &laneFakeRunner{}

		if got := runLaneMainTest([]string{"plan-execute", briefPath}, &stdout, &stderr, fake); got != 0 {
			ginkgo.Fail(fmt.Sprintf("expected exit 0, got %d (stderr=%s)", got, stderr.String()))
		}
		gomega.Expect(fake.calledMode).To(gomega.Equal("plan-execute"), fmt.Sprintf("expected forwarding to plan-execute, got %q", fake.calledMode))

	})
	ginkgo.It("run lane main plan execute forwards required edit paths from brief", func() {
		briefPath := writeBrief("# Generic brief\n\n" +
			"## Files to Modify\n\n" +
			"Exhaustive allowlist for this run:\n\n" +
			"| Path | Reason |\n" +
			"|---|---|\n" +
			"| `src/foo.go` | Update behavior. |\n" +
			"| `.planning/tracker.md` | Record accepted evidence. |\n")
		root := filepath.Dir(briefPath)

		var stdout, stderr bytes.Buffer

		fake := &laneFakeRunner{}

		code := runLaneMainTest([]string{"plan-execute", "--project-root", root, briefPath}, &stdout, &stderr, fake)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("expected exit 0, got %d stderr=%s stdout=%s", code, stderr.String(), stdout.String()))

		got := []string(fake.calledPlanOptions.RequiredEditPaths)
		want := []string{"src/foo.go", ".planning/tracker.md"}
		gomega.Expect(strings.Join(got, ",")).To(gomega.Equal(strings.Join(want, ",")), fmt.Sprintf("plan-execute required edit paths = %#v, want %#v; opts=%+v", got, want, fake.calledPlanOptions))

	})
	ginkgo.It("run lane main plan execute forwards atom intake scope paths", func() {
		root := ginkgo.GinkgoT().TempDir()
		briefPath := filepath.Join(root, "atom.md")
		absFooPath := filepath.Join(root, "src", "foo.go")
		brief := "# Atom\n\n" +
			"Status: pending\n\n" +
			"## Scope\n\n" +
			"- `" + absFooPath + "`\n" +
			"- `.planning/tracker.md`\n\n" +
			"## Acceptance\n\n" +
			"- The lane reaches the fake runner.\n"
		gomega.Expect(os.WriteFile(briefPath, []byte(brief), 0o644)).To(gomega.Succeed())

		var stdout, stderr bytes.Buffer

		fake := &laneFakeRunner{}

		code := runLaneMainTest([]string{"plan-execute", "--json", "--project-root", root, briefPath}, &stdout, &stderr, fake)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("expected exit 0, got %d stderr=%s stdout=%s", code, stderr.String(), stdout.String()))

		got := []string(fake.calledPlanOptions.RequiredEditPaths)
		want := []string{"src/foo.go", ".planning/tracker.md"}
		gomega.Expect(strings.Join(got, ",")).To(gomega.Equal(strings.Join(want, ",")), fmt.Sprintf("plan-execute required edit paths = %#v, want %#v; opts=%+v", got, want, fake.calledPlanOptions))
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring(`"state":"dispatchable"`), fmt.Sprintf("atom intake should report dispatchable scope, stdout=%s", stdout.String()))

	})
	ginkgo.It("run lane main atom intake blocks non dispatchable before runner", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.WriteFile(filepath.Join(root, "foo.go"), []byte("package main\n\nfunc done() {}\n"), 0o644)).To(gomega.Succeed())

		briefPath := filepath.Join(root, "atom.md")
		brief := "# Atom\n\n## Files To Modify\n\n| File | Change |\n|---|---|\n| `foo.go` | Add done helper. |\n\n## Required Edits\n\nIn `foo.go`, add:\n\n```go\nfunc done() {}\n```\n"
		gomega.Expect(os.WriteFile(briefPath, []byte(brief), 0o644)).To(gomega.Succeed())

		var stdout, stderr bytes.Buffer

		fake := &laneFakeRunner{}
		code := runLaneMainTest([]string{"plan-execute", "--json", "--project-root", root, briefPath}, &stdout, &stderr, fake)
		gomega.Expect(code).NotTo(gomega.Equal(0), fmt.Sprintf("expected atom intake to block dispatch"))
		gomega.Expect(fake.calledMode).To(gomega.Equal(""), fmt.Sprintf("fake runner should not be called, got mode %q", fake.calledMode))
		gomega.Expect(stderr.String()).To(gomega.ContainSubstring("atom intake blocked dispatch"), fmt.Sprintf("stderr should explain intake block, got:\n%s", stderr.String()))
		gomega.Expect(stderr.String()).To(gomega.ContainSubstring("source-satisfied"), fmt.Sprintf("stderr should explain intake block, got:\n%s", stderr.String()))
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring(`"type":"`+lanecontract.EventName(lanecontract.EventAtomIntake)+`"`), fmt.Sprintf("stdout should include atom intake event, got:\n%s", stdout.String()))
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring(`"dispatchable":false`), fmt.Sprintf("stdout should include atom intake event, got:\n%s", stdout.String()))

	})
	ginkgo.It("run lane main atom intake owns missing scope before runner", func() {
		root := ginkgo.GinkgoT().TempDir()
		briefPath := filepath.Join(root, "atom.md")
		brief := "# Atom\n\n## Required Edits\n\n- Change the banner text.\n"
		gomega.Expect(os.WriteFile(briefPath, []byte(brief), 0o644)).To(gomega.Succeed())

		var stdout, stderr bytes.Buffer

		fake := &laneFakeRunner{}
		code := runLaneMainTest([]string{"plan-execute", "--json", "--project-root", root, briefPath}, &stdout, &stderr, fake)
		gomega.Expect(code).NotTo(gomega.Equal(0), fmt.Sprintf("expected missing-scope atom intake to block dispatch"))
		gomega.Expect(fake.calledMode).To(gomega.Equal(""), fmt.Sprintf("fake runner should not be called, got mode %q", fake.calledMode))

		for _, want := range []string{
			`"type":"` + lanecontract.EventName(lanecontract.EventAtomIntake) + `"`,
			`"state":"missing-scope"`,
			`"workflow_id":"atom_intake"`,
			`"workflow_state":"terminal"`,
			`"workflow_terminal_state":"missing-scope"`,
			`"workflow_reason":"missing_scope"`,
		} {
			gomega.Expect(stdout.String()).To(gomega.ContainSubstring(want), fmt.Sprintf("stdout missing %s:\n%s", want, stdout.String()))

		}
		gomega.Expect(stderr.String()).To(gomega.ContainSubstring("atom intake blocked dispatch"), fmt.Sprintf("stderr should explain intake block, got:\n%s", stderr.String()))

	})
	ginkgo.It("run lane main atom intake recognizes implementation snippet without status", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.WriteFile(filepath.Join(root, "foo.go"), []byte("package main\n\nfunc done() {}\n"), 0o644)).To(gomega.Succeed())

		briefPath := filepath.Join(root, "atom.md")
		brief := "# Atom\n\n## Files To Modify\n\n| File | Change |\n|---|---|\n| `foo.go` | Add done helper. |\n\n## Implementation\n\nIn `foo.go`, insert:\n\n```go\nfunc done() {}\n```\n"
		gomega.Expect(os.WriteFile(briefPath, []byte(brief), 0o644)).To(gomega.Succeed())

		var stdout, stderr bytes.Buffer

		fake := &laneFakeRunner{}
		code := runLaneMainTest([]string{"plan-execute", "--json", "--project-root", root, briefPath}, &stdout, &stderr, fake)
		gomega.Expect(code).NotTo(gomega.Equal(0), fmt.Sprintf("expected implementation snippet intake to block dispatch"))
		gomega.Expect(fake.calledMode).To(gomega.Equal(""), fmt.Sprintf("fake runner should not be called, got mode %q", fake.calledMode))
		gomega.Expect(stderr.String()).To(gomega.ContainSubstring("source-satisfied"), fmt.Sprintf("stderr should explain source-satisfied implementation snippet, got:\n%s", stderr.String()))

	})
	ginkgo.It("run lane main atom intake blocks dirty scoped path before runner", func() {
		root := initGitRepoForStaleness()
		gitCommit(root, "src/foo.go", "package src\n", "initial")
		gomega.Expect(os.WriteFile(filepath.Join(root, "src/foo.go"), []byte("package src\n\nfunc dirty() {}\n"), 0o644)).To(gomega.Succeed())

		brief := "# Atom\n\nStatus: planned\n\n## Files To Modify\n\n| File | Change |\n|---|---|\n| `src/foo.go` | Modify source. |\n"
		briefPath := writeBriefFile(root, "atom.md", brief, time.Now().Add(time.Hour))

		var stdout, stderr bytes.Buffer

		fake := &laneFakeRunner{}
		code := runLaneMainTest([]string{"plan-execute", "--json", "--project-root", root, briefPath}, &stdout, &stderr, fake)
		gomega.Expect(code).NotTo(gomega.Equal(0), fmt.Sprintf("expected dirty atom scope to block dispatch"))
		gomega.Expect(fake.calledMode).To(gomega.Equal(""), fmt.Sprintf("fake runner should not be called, got mode %q", fake.calledMode))
		gomega.Expect(stderr.String()).To(gomega.ContainSubstring("blocked-dirty-files"), fmt.Sprintf("stderr should explain dirty scope block, got:\n%s", stderr.String()))
		gomega.Expect(stderr.String()).To(gomega.ContainSubstring("src/foo.go"), fmt.Sprintf("stderr should explain dirty scope block, got:\n%s", stderr.String()))
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring(`"state":"blocked-dirty-files"`), fmt.Sprintf("stdout should include intake and ledger events, got:\n%s", stdout.String()))
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring(`"type":"`+lanecontract.EventName(lanecontract.EventRunLedger)+`"`), fmt.Sprintf("stdout should include intake and ledger events, got:\n%s", stdout.String()))

	})
	ginkgo.It("run lane main atom intake allow dirty dispatches", func() {
		root := initGitRepoForStaleness()
		gitCommit(root, "src/foo.go", "package src\n", "initial")
		gomega.Expect(os.WriteFile(filepath.Join(root, "src/foo.go"), []byte("package src\n\nfunc dirty() {}\n"), 0o644)).To(gomega.Succeed())

		brief := "# Atom\n\nStatus: planned\n\n## Files To Modify\n\n| File | Change |\n|---|---|\n| `src/foo.go` | Modify source. |\n"
		briefPath := writeBriefFile(root, "atom.md", brief, time.Now().Add(time.Hour))

		var stdout, stderr bytes.Buffer

		fake := &laneFakeRunner{}
		code := runLaneMainTest([]string{"plan-execute", "--json", "--allow-dirty", "--project-root", root, briefPath}, &stdout, &stderr, fake)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("expected --allow-dirty to dispatch, got %d stderr=%s stdout=%s", code, stderr.String(), stdout.String()))
		gomega.Expect(fake.calledMode).To(gomega.Equal("plan-execute"), fmt.Sprintf("fake runner should be called, got mode %q", fake.calledMode))
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring(`"state":"dispatchable"`), fmt.Sprintf("stdout should include dispatchable atom intake event, got:\n%s", stdout.String()))

	})
	ginkgo.It("run lane main implement forwards", func() {
		briefPath := writeBrief("# Implement brief\n")

		var stdout, stderr bytes.Buffer

		fake := &laneFakeRunner{}

		if got := runLaneMainTest([]string{"implement", briefPath}, &stdout, &stderr, fake); got != 0 {
			ginkgo.Fail(fmt.Sprintf("expected exit 0, got %d", got))
		}
		gomega.Expect(fake.calledMode).To(gomega.Equal("implement"), fmt.Sprintf("expected forwarding to implement, got %q", fake.calledMode))

	})
	ginkgo.It("run lane main composes preamble postamble", func() {
		briefBody := "# Donkey atom\nFiles To Modify: animal_types.h\n"
		briefPath := writeBrief(briefBody)

		var stdout, stderr bytes.Buffer

		fake := &laneFakeRunner{}

		if got := runLaneMainTest([]string{"plan-execute", briefPath}, &stdout, &stderr, fake); got != 0 {
			ginkgo.Fail(fmt.Sprintf("expected exit 0, got %d (stderr=%s)", got, stderr.String()))
		}

		prompt := fake.calledPrompt
		gomega.Expect(prompt).To(gomega.ContainSubstring(briefBody), fmt.Sprintf("composed prompt must include the brief body verbatim, got: %s", prompt[:min(200, len(prompt))]))

	})
	ginkgo.It("run lane main injects automatic context pack before brief", func() {
		briefBody := "# Routing atom\n\nFiles To Modify:\n- internal/surfaces/tui/app/prompt_routing.go\n"
		briefPath := writeBrief(briefBody)

		var stdout, stderr bytes.Buffer

		fake := &laneFakeRunner{}
		cfg := config.Default()

		cfg.Workspace.Root = filepath.Dir(briefPath)
		cfg.Workspace.EnableWrites = true

		stubLaneBriefLint(passingLaneBriefLint)

		deps := laneDeps{
			loadConfig:     func(string) (config.Config, error) { return cfg, nil },
			runPlanExecute: fake.plan,
			runImplement:   fake.implement,
			defaultPromptRun: func(config.Config, string, cmdruntime.RunOptions, io.Writer) (string, error) {
				return "", nil
			},
			buildContextPack: func(opts laneOptions, got config.Config, stderr io.Writer) string {
				gomega.Expect(opts.Mode).To(gomega.Equal("implement"), fmt.Sprintf("context pack saw mode %q, want implement", opts.Mode))
				gomega.Expect(got.Workspace.Root).To(gomega.Equal(cfg.Workspace.Root), fmt.Sprintf("context pack saw root %q, want %q", got.Workspace.Root, cfg.Workspace.Root))

				return "## Context Pack\n\n- `internal/surfaces/tui/app/prompt_routing.go` (editable): matched task terms\n"
			},
		}
		code := runLaneMainWithDeps([]string{"implement", "--project-root", cfg.Workspace.Root, briefPath}, &stdout, &stderr, deps)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("expected exit 0, got %d (stderr=%s)", code, stderr.String()))

		prompt := fake.calledPrompt
		contextIdx := strings.Index(prompt, "## Context Pack")
		briefIdx := strings.Index(prompt, briefBody)
		gomega.Expect(contextIdx < 0).To(gomega.BeFalse(), fmt.Sprintf("composed prompt missing context pack:\n%s", prompt))
		gomega.Expect(briefIdx < 0).To(gomega.BeFalse(), fmt.Sprintf("composed prompt missing brief body:\n%s", prompt))
		gomega.Expect(contextIdx > briefIdx).To(gomega.BeFalse(), fmt.Sprintf("context pack should precede brief body"))
		gomega.Expect(prompt).To(gomega.ContainSubstring(briefBodyStartMarker+"\n"+briefBody), fmt.Sprintf("brief body should remain marker-scoped after context pack injection"))

	})
	ginkgo.It("render lane context pack section adds fallback when no targets", func() {
		section, err := renderLaneContextPackSection(workspace.ToolResult{
			Tool:    "context_pack",
			Summary: "Context pack wrote .idiom/comprehension/views/empty.json with 0 candidate file(s)",
			Output:  `{"pack_id":"empty","candidate_files":[],"candidate_symbols":[],"tests":[]}`,
		})
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
			gomega.Expect(section).To(gomega.ContainSubstring(want), fmt.Sprintf("fallback section missing %q:\n%s", want, section))

		}
	})
	ginkgo.It("render lane context discovery fallback section includes objective", func() {
		section := renderLaneContextDiscoveryFallbackSection(laneOptions{
			Title: "Replace banner path with alias",
		}, "context index unavailable\nretry timed out")
		for _, want := range []string{
			"## Context Discovery Fallback",
			"Replace banner path with alias",
			"context index unavailable retry timed out",
			"before the first edit-bearing action",
			"before the first `submit_plan` or write action",
			"`search_files`",
			"`search_content`",
			"`read_file`",
		} {
			gomega.Expect(section).To(gomega.ContainSubstring(want), fmt.Sprintf("fallback section missing %q:\n%s", want, section))

		}

		for _, notWant := range []string{"before writing", "before the first write"} {
			gomega.Expect(section).NotTo(gomega.ContainSubstring(notWant), fmt.Sprintf("fallback prompt should not use implementation-only phrase %q:\n%s", notWant, section))

		}
	})
	ginkgo.It("run lane main telemetry matches doctor readiness profile", func() {
		root := ginkgo.GinkgoT().TempDir()
		ginkgo.GinkgoT().
			Chdir(root)
		ginkgo.GinkgoT().
			Setenv("IDIOM_RUNNER_SOCKET", filepath.Join(root, "missing-runner.sock"))

		configPath := filepath.Join(root, ".idiom", "config.json")
		gomega.Expect(os.MkdirAll(filepath.Dir(configPath), 0o755)).To(gomega.Succeed())

		configBody := `{
  "workspace": {"root": "` + root + `", "enable_writes": true},
  "llama_server": {"model": "coder", "kind": "mlx", "port": 47781, "ctx_size": 8192},
  "chat": {"max_tokens": 2048},
  "route_classifier": {"enabled": false}
}`
		gomega.Expect(os.WriteFile(configPath, []byte(configBody), 0o644)).To(gomega.Succeed())

		stageDoctorBackends(root)

		briefPath := filepath.Join(root, "brief.md")
		gomega.Expect(os.WriteFile(briefPath, []byte("# Implement brief\n\nNo prompt secret should appear in telemetry.\n"), 0o644)).To(gomega.Succeed())

		rec, err := telemetry.Open(telemetry.Options{
			Dir:           filepath.Join(root, "telemetry"),
			Command:       "lane:implement",
			Args:          []string{"lane", "implement", "--config", configPath, "--prompt", "SECRET raw prompt body"},
			WorkspaceRoot: root,
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("telemetry.Open: %v", err))

		var stdout, stderr bytes.Buffer

		fake := &laneFakeRunner{}

		stubLaneBriefLint(passingLaneBriefLint)

		deps := laneDeps{
			loadConfig:     config.Load,
			runPlanExecute: fake.plan,
			runImplement:   fake.implement,
			defaultPromptRun: func(config.Config, string, cmdruntime.RunOptions, io.Writer) (string, error) {
				return "", nil
			},
			buildContextPack: func(laneOptions, config.Config, io.Writer) string { return "" },
			telemetry:        rec,
		}
		code := runLaneMainWithDeps([]string{"implement", "--json", "--no-context-pack", "--config", configPath, "--project-root", root, briefPath}, &stdout, &stderr, deps)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("expected exit 0, got %d stderr=%s stdout=%s", code, stderr.String(), stdout.String()))

		writerRouteEventName, err := writerroute.CommandEventName(writerroute.CommandSurfaceLane, writerroute.EventWriterRouteDecision)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("writer route event name: %v", err))
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring(`"type":"`+writerRouteEventName+`"`), fmt.Sprintf("lane JSON should report writer route decision with role aliases, got %s", stdout.String()))
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring(`"writer_mode":"agentic_guarded"`), fmt.Sprintf("lane JSON should report writer route decision with role aliases, got %s", stdout.String()))
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring(`"role_aliases"`), fmt.Sprintf("lane JSON should report writer route decision with role aliases, got %s", stdout.String()))
		gomega.Expect(rec.CloseWithFields(0, nil)).To(gomega.Succeed())

		doctorProfiles, err := readiness.BuildProfiles(configPath, "implement", nil)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("build doctor readiness: %v", err))

		doctor := doctorProfiles[0]
		records := readMainTelemetryRecords(rec.Path())
		session := findLaneTelemetryRecord(records, "session_start")
		gomega.Expect(session).NotTo(gomega.BeNil(), fmt.Sprintf("session_start should identify lane implement command, got %#v", session))
		gomega.Expect(session["command"]).To(gomega.Equal("lane:implement"), fmt.Sprintf("session_start should identify lane implement command, got %#v", session))

		start := findLaneTelemetryRecord(records, "lane_start")
		gomega.Expect(start).NotTo(gomega.Equal(nil), fmt.Sprintf("missing lane_start telemetry in %#v", records))
		gomega.Expect(start["readiness_profile"]).To(gomega.Equal(doctor.Name), fmt.Sprintf("lane_start telemetry should match doctor readiness metrics\nlane=%#v\ndoctor=%#v", start, doctor))
		gomega.Expect(start["readiness_state"]).To(gomega.Equal(doctor.State), fmt.Sprintf("lane_start telemetry should match doctor readiness metrics\nlane=%#v\ndoctor=%#v", start, doctor))
		gomega.Expect(start["backend_state"]).To(gomega.Equal(doctor.Metrics.BackendState), fmt.Sprintf("lane_start telemetry should match doctor readiness metrics\nlane=%#v\ndoctor=%#v", start, doctor))
		gomega.Expect(start["queue_state"]).To(gomega.Equal(doctor.Metrics.QueueState), fmt.Sprintf("lane_start telemetry should match doctor readiness metrics\nlane=%#v\ndoctor=%#v", start, doctor))
		gomega.Expect(start["context_budget_state"]).To(gomega.Equal(doctor.Metrics.ContextBudgetState), fmt.Sprintf("lane_start telemetry should match doctor readiness metrics\nlane=%#v\ndoctor=%#v", start, doctor))
		gomega.Expect(start["route_classifier_state"]).To(gomega.Equal(doctor.Metrics.ClassifierState), fmt.Sprintf("lane_start telemetry should match doctor readiness metrics\nlane=%#v\ndoctor=%#v", start, doctor))
		gomega.Expect(intFromTelemetry(start["hard_failures"])).To(gomega.Equal(doctor.Metrics.HardFailures), fmt.Sprintf("lane_start telemetry should match doctor readiness metrics\nlane=%#v\ndoctor=%#v", start, doctor))
		gomega.Expect(intFromTelemetry(start["soft_degradations"])).To(gomega.Equal(doctor.Metrics.SoftDegradations), fmt.Sprintf("lane_start telemetry should match doctor readiness metrics\nlane=%#v\ndoctor=%#v", start, doctor))
		gomega.Expect(start["enforcement"]).To(gomega.Equal("report_only"), fmt.Sprintf("P6.4 must not escalate readiness enforcement, got %#v", start))

		aliases, ok := start["role_aliases"].(map[string]any)
		gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("lane_start should carry doctor-compatible role aliases, got %#v doctor=%#v", start["role_aliases"], doctor.RoleAliases))
		gomega.Expect(aliases["executor"]).To(gomega.Equal(doctor.RoleAliases["executor"]), fmt.Sprintf("lane_start should carry doctor-compatible role aliases, got %#v doctor=%#v", start["role_aliases"], doctor.RoleAliases))

		localOps, ok := start["local_ops"].(map[string]any)
		gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("local_ops should expose doctor-compatible state names where practical, got %#v doctor=%#v", localOps, doctor.Metrics))
		gomega.Expect(localOps["context_budget_state"]).To(gomega.Equal(doctor.Metrics.ContextBudgetState), fmt.Sprintf("local_ops should expose doctor-compatible state names where practical, got %#v doctor=%#v", localOps, doctor.Metrics))
		gomega.Expect(localOps["route_classifier_state"]).To(gomega.Equal(doctor.Metrics.ClassifierState), fmt.Sprintf("local_ops should expose doctor-compatible state names where practical, got %#v doctor=%#v", localOps, doctor.Metrics))
		gomega.Expect(localOps["backend_state"]).To(gomega.Equal(doctor.Metrics.BackendState), fmt.Sprintf("local_ops should expose doctor-compatible state names where practical, got %#v doctor=%#v", localOps, doctor.Metrics))

		rendered, _ := json.Marshal(records)
		for _, forbidden := range []string{"SECRET raw prompt body", "finance", "healthcare/life sciences", "public-sector", "enterprise SaaS", "tooling-vendor", "local-model/private-code", "open-source"} {
			gomega.Expect(string(rendered)).NotTo(gomega.ContainSubstring(forbidden), fmt.Sprintf("lane telemetry should remain metadata-only and client-neutral; found %q in %s", forbidden, rendered))

		}
	})
	ginkgo.It("run lane main plan execute json emits writer route decision", func() {
		root := ginkgo.GinkgoT().TempDir()
		briefPath := filepath.Join(root, "brief.md")
		gomega.Expect(os.WriteFile(briefPath, []byte("# Plan execute brief\n\nUpdate the route decision tests.\n"), 0o644)).To(gomega.Succeed())

		var stdout, stderr bytes.Buffer

		fake := &laneFakeRunner{}
		code := runLaneMainTest([]string{"plan-execute", "--json", "--project-root", root, briefPath}, &stdout, &stderr, fake)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("expected exit 0, got %d stderr=%s stdout=%s", code, stderr.String(), stdout.String()))

		writerRouteEventName, err := writerroute.CommandEventName(writerroute.CommandSurfaceLane, writerroute.EventWriterRouteDecision)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("writer route event name: %v", err))

		events := jsoneventstest.DecodeLines(ginkgo.GinkgoT(), stdout.String())
		event, ok := jsoneventstest.FindByType(events, writerRouteEventName)
		gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("missing lane writer route event in %s", stdout.String()))
		gomega.Expect(event["writer_mode"]).To(gomega.Equal("plan_submission"), fmt.Sprintf("unexpected plan-execute lane route event: %#v", event))
		gomega.Expect(event["lane"]).To(gomega.Equal("plan-execute"), fmt.Sprintf("unexpected plan-execute lane route event: %#v", event))
		gomega.Expect(event["verification_owner"]).To(gomega.Equal("planner_owned"), fmt.Sprintf("unexpected plan-execute lane route event: %#v", event))
		gomega.Expect(event["task_family"]).To(gomega.Equal(agent.PlanThenExecutePlannerTaskFamily), fmt.Sprintf("plan-execute lane route event should carry task_family, got %#v", event))
		gomega.Expect(jsonEventStringSliceContains(event["reason_codes"], "route.multi_file_plan_execute")).To(gomega.BeTrue(), fmt.Sprintf("plan-execute lane route should explain multi-file plan-execute dispatch: %#v", event))

	})
	ginkgo.It("run lane main no context pack skips automatic context pack", func() {
		briefPath := writeBrief("# Routing atom\n\nFiles To Modify:\n- internal/surfaces/tui/app/prompt_routing.go\n")

		var stdout, stderr bytes.Buffer

		fake := &laneFakeRunner{}
		cfg := config.Default()

		stubLaneBriefLint(passingLaneBriefLint)

		calls := 0
		deps := laneDeps{
			loadConfig:       func(string) (config.Config, error) { return cfg, nil },
			runPlanExecute:   fake.plan,
			runImplement:     fake.implement,
			defaultPromptRun: func(config.Config, string, cmdruntime.RunOptions, io.Writer) (string, error) { return "", nil },
			buildContextPack: func(laneOptions, config.Config, io.Writer) string {
				calls++
				return "## Context Pack\n\nshould not appear\n"
			},
		}
		code := runLaneMainWithDeps([]string{"implement", "--no-context-pack", briefPath}, &stdout, &stderr, deps)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("expected exit 0, got %d (stderr=%s)", code, stderr.String()))
		gomega.Expect(calls).To(gomega.Equal(0), fmt.Sprintf("context pack builder should not run when --no-context-pack is set, got %d call(s)", calls))
		gomega.Expect(fake.calledPrompt).NotTo(gomega.ContainSubstring("should not appear"), fmt.Sprintf("context pack section leaked into prompt"))

	})
	ginkgo.It("run lane main skips context pack for refine scratchpad meta brief", func() {
		root := ginkgo.GinkgoT().TempDir()
		metaDir := filepath.Join(root, ".idiom", "refine", "Atom", "iter-1")
		gomega.Expect(os.MkdirAll(metaDir, 0o755)).To(gomega.Succeed())

		briefPath := filepath.Join(metaDir, "meta-patch.md")
		gomega.Expect(os.WriteFile(briefPath, []byte("# META - Patch brief\n\nFiles To Modify:\n- .idiom/refine/Atom/iter-2/draft.md\n"), 0o644)).To(gomega.Succeed())

		var stdout, stderr bytes.Buffer

		fake := &laneFakeRunner{}
		cfg := config.Default()

		cfg.Workspace.Root = root

		stubLaneBriefLint(passingLaneBriefLint)

		calls := 0
		deps := laneDeps{
			loadConfig:       func(string) (config.Config, error) { return cfg, nil },
			runPlanExecute:   fake.plan,
			runImplement:     fake.implement,
			defaultPromptRun: func(config.Config, string, cmdruntime.RunOptions, io.Writer) (string, error) { return "", nil },
			buildContextPack: func(laneOptions, config.Config, io.Writer) string {
				calls++
				return "## Context Pack\n\nshould not appear\n"
			},
		}
		code := runLaneMainWithDeps([]string{"implement", "--project-root", root, briefPath}, &stdout, &stderr, deps)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("expected exit 0, got %d (stderr=%s)", code, stderr.String()))
		gomega.Expect(calls).To(gomega.Equal(0), fmt.Sprintf("context pack builder should not run for refine scratchpad meta briefs, got %d call(s)", calls))
		gomega.Expect(fake.calledPrompt).NotTo(gomega.ContainSubstring("should not appear"), fmt.Sprintf("context pack section leaked into prompt"))

	})
	ginkgo.It("render lane context pack section omits cache artifact path", func() {
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

		section, err := renderLaneContextPackSection(workspace.ToolResult{
			Tool:    "context_pack",
			Summary: "Context pack wrote .idiom/comprehension/views/abc123.json with 1 candidate file(s)",
			Output:  output,
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("render context pack: %v", err))
		gomega.Expect(section).NotTo(gomega.ContainSubstring(".idiom/comprehension/views/abc123.json"), fmt.Sprintf("context pack section should not expose cache artifact as a workspace evidence path:\n%s", section))

		for _, want := range []string{"context view artifact", "`internal/surfaces/tui/app/prompt_routing.go`", "`runtimeBriefForRoute`", "`go test ./internal/surfaces/tui/app`"} {
			gomega.Expect(section).To(gomega.ContainSubstring(want), fmt.Sprintf("expected %q in context section:\n%s", want, section))

		}
	})
	ginkgo.It("build lane context pack section seeds required edit paths", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/lanecontext\n\ngo 1.22\n"), 0o644)).To(gomega.Succeed())
		gomega.Expect(os.MkdirAll(filepath.Join(root, "pkg"), 0o755)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(root, "pkg", "target.go"), []byte("package pkg\n\nfunc Target() string { return \"target\" }\n"), 0o644)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(root, "pkg", "noisy.go"), []byte("package pkg\n\nfunc Noisy() string { return \"noisy\" }\n"), 0o644)).To(gomega.Succeed())

		mgr := workspace.NewManager(config.Workspace{Root: root, CWD: root})
		if result := mgr.Execute(context.Background(), workspace.ToolRequest{Tool: "context_index", Language: "go"}); result.Error != "" {
			ginkgo.Fail(fmt.Sprintf("context_index returned error: %v\n%s", result.Error, result.Output))
		}

		brief := "# Lane atom\n\n## Task\n\nPlan a change to Noisy.\n\n## Files To Modify\n\n| File | Change |\n|---|---|\n| `pkg/target.go` | edit target |\n"
		briefPath := filepath.Join(root, "atom.md")
		gomega.Expect(os.WriteFile(briefPath, []byte(brief), 0o644)).To(gomega.Succeed())

		var stderr bytes.Buffer

		section := buildLaneContextPackSection(laneOptions{
			Mode:        "plan-execute",
			BriefPath:   briefPath,
			ProjectRoot: root,
			Title:       "Lane atom",
		}, config.Config{Workspace: config.Workspace{Root: root, CWD: root}}, &stderr)
		gomega.Expect(section).To(gomega.ContainSubstring("`pkg/target.go`"), fmt.Sprintf("automatic context pack should include Files To Modify path, stderr=%s\nsection:\n%s", stderr.String(), section))

	})
	ginkgo.It("run lane main composes project overlay", func() {
		briefBody := "# atom\nFiles: x"
		briefPath := writeBrief(briefBody)
		projectRoot := filepath.Dir(filepath.Dir(briefPath))
		overlayDir := filepath.Join(filepath.Dir(briefPath), ".idiom", "prompts", "lanes", "plan-execute")
		gomega.Expect(os.MkdirAll(overlayDir, 0o755)).To(gomega.Succeed())

		overlayBody := "PROJECT_OVERLAY_SENTINEL: Colony-specific guidance"
		gomega.Expect(os.WriteFile(filepath.Join(overlayDir, "preamble.md"), []byte(overlayBody), 0o644)).To(gomega.Succeed())

		var stdout, stderr bytes.Buffer

		fake := &laneFakeRunner{}
		args := []string{"plan-execute", "--project-root", filepath.Dir(briefPath), briefPath}

		_ = projectRoot

		if got := runLaneMainTest(args, &stdout, &stderr, fake); got != 0 {
			ginkgo.Fail(fmt.Sprintf("expected exit 0, got %d (stderr=%s)", got, stderr.String()))
		}

		prompt := fake.calledPrompt
		gomega.Expect(prompt).To(gomega.ContainSubstring("PROJECT_OVERLAY_SENTINEL"), fmt.Sprintf("composed prompt must include project overlay text; got: %s", prompt[:min(400, len(prompt))]))
		gomega.Expect(strings.Index(prompt, "PROJECT_OVERLAY_SENTINEL") > strings.Index(prompt, briefBody)).To(gomega.BeFalse(), fmt.Sprintf("project overlay must precede the brief body in the composed prompt"))

	})
	ginkgo.It("run lane main plan execute honors backends json executor", func() {
		briefPath := writeBrief("# atom\n")
		root := filepath.Dir(briefPath)
		gomega.Expect(os.MkdirAll(filepath.Join(root, ".idiom"), 0o755)).To(gomega.Succeed())

		body := `{"profiles": {"local": {"models": [{"alias": "custom-coder-7b"}], "proxy": {"default_executor": "custom-coder-7b"}}}}`
		gomega.Expect(os.WriteFile(filepath.Join(root, ".idiom", "backends.json"), []byte(body), 0o644)).To(gomega.Succeed())

		var stdout, stderr bytes.Buffer

		fake := &laneFakeRunner{}
		args := []string{"plan-execute", "--project-root", root, briefPath}

		if got := runLaneMainTest(args, &stdout, &stderr, fake); got != 0 {
			ginkgo.Fail(fmt.Sprintf("expected exit 0, got %d (stderr=%s)", got, stderr.String()))
		}
		gomega.Expect(fake.calledPlanOptions.Model).To(gomega.Equal("custom-coder-7b"), fmt.Sprintf("plan-execute auto-route must use backends.json default_executor; opts=%+v", fake.calledPlanOptions))

	})
	ginkgo.It("run lane main coder shortcut honors backends json", func() {
		briefPath := writeBrief("# atom\n")
		root := filepath.Dir(briefPath)
		gomega.Expect(os.MkdirAll(filepath.Join(root, ".idiom"), 0o755)).To(gomega.Succeed())

		body := `{"profiles": {"local": {"models": [{"alias": "fancy-coder-13b"}], "proxy": {"default_executor": "fancy-coder-13b"}}}}`
		gomega.Expect(os.WriteFile(filepath.Join(root, ".idiom", "backends.json"), []byte(body), 0o644)).To(gomega.Succeed())

		var stdout, stderr bytes.Buffer

		fake := &laneFakeRunner{}

		args := []string{"implement", "--project-root", root, "--coder", briefPath}
		if got := runLaneMainTest(args, &stdout, &stderr, fake); got != 0 {
			ginkgo.Fail(fmt.Sprintf("expected exit 0, got %d (stderr=%s)", got, stderr.String()))
		}
		gomega.Expect(argsContain(fake.calledArgs, "--model", "fancy-coder-13b")).To(gomega.BeTrue(), fmt.Sprintf("--coder must resolve via backends.json; forwarded: %v", fake.calledArgs))

	})
	ginkgo.It("run lane main omits model flag when no backends json", func() {
		briefPath := writeBrief("# atom\n")

		var stdout, stderr bytes.Buffer

		fake := &laneFakeRunner{}

		if got := runLaneMainTest([]string{"plan-execute", briefPath}, &stdout, &stderr, fake); got != 0 {
			ginkgo.Fail(fmt.Sprintf("expected exit 0, got %d", got))
		}
		gomega.Expect(fake.calledPlanOptions.Model).To(gomega.Equal(""), fmt.Sprintf("absent backends.json, plan-execute must not inject a model override; opts=%+v", fake.calledPlanOptions))

	})
	ginkgo.It("run lane main backend profile flag selects profile", func() {
		briefPath := writeBrief("# atom\n")
		root := filepath.Dir(briefPath)
		gomega.Expect(os.MkdirAll(filepath.Join(root, ".idiom"), 0o755)).To(gomega.Succeed())

		body := `{"profiles": {
		"prod": {"models": [{"alias": "prod-coder"}], "proxy": {"default_executor": "prod-coder"}},
		"dev":  {"models": [{"alias": "dev-coder"}],  "proxy": {"default_executor": "dev-coder"}}
	}}`
		gomega.Expect(os.WriteFile(filepath.Join(root, ".idiom", "backends.json"), []byte(body), 0o644)).To(gomega.Succeed())

		var stdout, stderr bytes.Buffer

		fake := &laneFakeRunner{}
		args := []string{"plan-execute", "--project-root", root, "--backend-profile", "dev", briefPath}

		if got := runLaneMainTest(args, &stdout, &stderr, fake); got != 0 {
			ginkgo.Fail(fmt.Sprintf("expected exit 0, got %d (stderr=%s)", got, stderr.String()))
		}
		gomega.Expect(fake.calledPlanOptions.Model).To(gomega.Equal("dev-coder"), fmt.Sprintf("--backend-profile dev must resolve to dev-coder; opts=%+v", fake.calledPlanOptions))

	})
	ginkgo.It("run lane main backend profile planner selects plan execute primary", func() {
		briefPath := writeBrief("# atom\n")
		root := filepath.Dir(briefPath)
		gomega.Expect(os.MkdirAll(filepath.Join(root, ".idiom"), 0o755)).To(gomega.Succeed())

		body := `{"profiles": {
		"qwen-27b-pair": {"models": [
			{"alias": "qwen3.6-27b", "roles": ["planner"]},
			{"alias": "qwen3-coder-30b", "roles": ["executor"]}
		], "proxy": {"default_planner": "qwen3.6-27b", "default_executor": "qwen3-coder-30b"}}
	}}`
		gomega.Expect(os.WriteFile(filepath.Join(root, ".idiom", "backends.json"), []byte(body), 0o644)).To(gomega.Succeed())

		var stdout, stderr bytes.Buffer

		fake := &laneFakeRunner{}
		args := []string{"plan-execute", "--project-root", root, "--backend-profile", "qwen-27b-pair", briefPath}

		if got := runLaneMainTest(args, &stdout, &stderr, fake); got != 0 {
			ginkgo.Fail(fmt.Sprintf("expected exit 0, got %d (stderr=%s)", got, stderr.String()))
		}
		gomega.Expect(fake.calledPlanOptions.Model).To(gomega.Equal("qwen3.6-27b"), fmt.Sprintf("--backend-profile qwen-27b-pair must resolve plan-execute to planner; opts=%+v", fake.calledPlanOptions))

	})
	ginkgo.It("run lane main explicit coder uses executor with planner profile", func() {
		briefPath := writeBrief("# atom\n")
		root := filepath.Dir(briefPath)
		gomega.Expect(os.MkdirAll(filepath.Join(root, ".idiom"), 0o755)).To(gomega.Succeed())

		body := `{"profiles": {
		"qwen-27b-pair": {"models": [
			{"alias": "qwen3.6-27b", "roles": ["planner"]},
			{"alias": "qwen3-coder-30b", "roles": ["executor"]}
		], "proxy": {"default_planner": "qwen3.6-27b", "default_executor": "qwen3-coder-30b"}}
	}}`
		gomega.Expect(os.WriteFile(filepath.Join(root, ".idiom", "backends.json"), []byte(body), 0o644)).To(gomega.Succeed())

		var stdout, stderr bytes.Buffer

		fake := &laneFakeRunner{}
		args := []string{"plan-execute", "--project-root", root, "--backend-profile", "qwen-27b-pair", "--coder", briefPath}

		if got := runLaneMainTest(args, &stdout, &stderr, fake); got != 0 {
			ginkgo.Fail(fmt.Sprintf("expected exit 0, got %d (stderr=%s)", got, stderr.String()))
		}
		gomega.Expect(fake.calledPlanOptions.Model).To(gomega.Equal("qwen3-coder-30b"), fmt.Sprintf("--coder must keep using the executor role; opts=%+v", fake.calledPlanOptions))

	})
	ginkgo.It("run lane main explicit model beats backends json", func() {
		briefPath := writeBrief("# atom\n")
		root := filepath.Dir(briefPath)
		gomega.Expect(os.MkdirAll(filepath.Join(root, ".idiom"), 0o755)).To(gomega.Succeed())

		body := `{"profiles": {"local": {"proxy": {"default_executor": "project-default-coder"}}}}`
		gomega.Expect(os.WriteFile(filepath.Join(root, ".idiom", "backends.json"), []byte(body), 0o644)).To(gomega.Succeed())

		var stdout, stderr bytes.Buffer

		fake := &laneFakeRunner{}
		args := []string{"plan-execute", "--project-root", root, "--model", "operator-override", briefPath}

		if got := runLaneMainTest(args, &stdout, &stderr, fake); got != 0 {
			ginkgo.Fail(fmt.Sprintf("expected exit 0, got %d", got))
		}
		gomega.Expect(fake.calledPlanOptions.Model).To(gomega.Equal("operator-override"), fmt.Sprintf("--model must beat backends.json default_executor; opts=%+v", fake.calledPlanOptions))

	})
	ginkgo.It("run lane main embeds canonical plan execute preamble", func() {
		briefBody := "# atom\nFiles To Modify: x\n"
		briefPath := writeBrief(briefBody)

		var stdout, stderr bytes.Buffer

		fake := &laneFakeRunner{}

		if got := runLaneMainTest([]string{"plan-execute", briefPath}, &stdout, &stderr, fake); got != 0 {
			ginkgo.Fail(fmt.Sprintf("expected exit 0, got %d (stderr=%s)", got, stderr.String()))
		}

		prompt := fake.calledPrompt
		normalizedPrompt := strings.Join(strings.Fields(prompt), " ")
		required := []string{
			"Plan-Then-Execute Lane Brief",
			"You are the PLANNER",
			"submit_plan",
			"replace_all:true",
			"full body for a missing/new file",
			"Existing source/test files must use bounded line-range or substring edits",
			"missing body evidence",
			"required paths still need",
			"source-body evidence",
			"this does not waive the source-body obligation",
			"exact-plan-ready/source-satisfied",
			"Every edit body must be executable final content",
			"no placeholders",
			"complete replacement for the inclusive",
		}

		for _, marker := range required {
			gomega.Expect(normalizedPrompt).To(gomega.ContainSubstring(strings.Join(strings.Fields(marker), " ")), fmt.Sprintf("plan-execute composed prompt missing canonical preamble marker %q; head=%s", marker, prompt[:min(400, len(prompt))]))

		}

		for _, forbidden := range []string{
			"sufficient source evidence for the plan",
			"Exact anchors, current-state anchors, full new-file bodies, explicit replacement edit shapes, unified patches, and ranked packet evidence are sufficient planning evidence",
			"if the location is known from the packet, plan it directly",
		} {
			gomega.Expect(normalizedPrompt).NotTo(gomega.ContainSubstring(strings.Join(strings.Fields(forbidden), " ")), fmt.Sprintf("plan-execute composed prompt retained ambiguous source-evidence phrase %q:\n%s", forbidden, prompt))

		}
		gomega.Expect(strings.Index(prompt, "Plan-Then-Execute Lane Brief") > strings.Index(prompt, briefBody)).To(gomega.BeFalse(), fmt.Sprintf("canonical preamble must precede the brief body"))

	})
	ginkgo.It("run lane main embeds canonical implement preamble", func() {
		briefBody := "# atom\nFiles To Modify: x\n"
		briefPath := writeBrief(briefBody)

		var stdout, stderr bytes.Buffer

		fake := &laneFakeRunner{}

		if got := runLaneMainTest([]string{"implement", briefPath}, &stdout, &stderr, fake); got != 0 {
			ginkgo.Fail(fmt.Sprintf("expected exit 0, got %d (stderr=%s)", got, stderr.String()))
		}

		prompt := fake.calledPrompt
		required := []string{
			"Implementation Lane Brief",
			"write-capable implementation contract",
			"named_gap",

			"Do NOT call",
			"submit_plan",
		}

		for _, marker := range required {
			gomega.Expect(prompt).To(gomega.ContainSubstring(marker), fmt.Sprintf("implement composed prompt missing canonical preamble marker %q; head=%s", marker, prompt[:min(400, len(prompt))]))

		}
	})
	ginkgo.It("canonical implement preamble uses capability lookup language", func() {
		body, err := os.ReadFile(cliAssetPath("lanes", "implement", "preamble.md"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read implement preamble: %v", err))

		preamble := string(body)
		normalized := strings.Join(strings.Fields(preamble), " ")

		for _, want := range []string{
			"source sanity",
			"symbol navigation or body inspection",
			"reference/callsite evidence",
			"project-declared read-only evidence",
			"exact text/range confirmation",
		} {
			gomega.Expect(normalized).To(gomega.ContainSubstring(want), fmt.Sprintf("implement preamble missing capability-oriented lookup language %q:\n%s", want, preamble))

		}

		for _, forbidden := range []string{
			"code_" + "navigate",
			"<lang>_" + "find_symbol",
			"<lang>_" + "read_symbol",
			"<lang>_" + "workspace_symbol",
			"<lang>_" + "references",
			"*_" + "file_outline",
			"raw " + "read_file",
			"Reserve " + "read_file",
		} {
			gomega.Expect(preamble).NotTo(gomega.ContainSubstring(forbidden), fmt.Sprintf("implement preamble still contains raw lookup strategy menu term %q:\n%s", forbidden, preamble))

		}
	})
	ginkgo.DescribeTable("appends the canonical postamble after the brief",
		func(mode, marker string) {
			briefPath := writeBrief("# atom\n")
			var stdout, stderr bytes.Buffer
			fake := &laneFakeRunner{}

			gomega.Expect(runLaneMainTest([]string{mode, briefPath}, &stdout, &stderr, fake)).To(gomega.Equal(0))
			prompt := fake.calledPrompt
			gomega.Expect(prompt).To(gomega.ContainSubstring(marker))
			briefIndex := strings.Index(prompt, "# atom")
			postambleIndex := strings.Index(prompt, marker)
			gomega.Expect(briefIndex).To(gomega.BeNumerically(">=", 0))
			gomega.Expect(postambleIndex).To(gomega.BeNumerically(">", briefIndex))
		},
		ginkgo.Entry("plan-execute", "plan-execute", "Plan-Then-Execute Closeout"),
		ginkgo.Entry("implement", "implement", "Implementation Closeout"),
	)
	ginkgo.It("canonical plan execute postamble allows source satisfied closeout", func() {
		body, err := os.ReadFile(cliAssetPath("lanes", "plan-execute", "postamble.md"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read plan-execute postamble: %v", err))

		postamble := string(body)
		for _, want := range []string{
			"`submit_plan` has been called successfully",
			"`Decision: source-satisfied`",
			"every required edit path",
			"`<<QUESTION>>:` blocker",
			"Do not submit verification",
			"placeholder edits",
		} {
			gomega.Expect(postamble).To(gomega.ContainSubstring(want), fmt.Sprintf("plan-execute postamble missing terminal contract language %q:\n%s", want, postamble))

		}
		gomega.Expect(postamble).NotTo(gomega.ContainSubstring("complete only after `submit_plan`"), fmt.Sprintf("plan-execute postamble must not exclude source-satisfied as a terminal outcome:\n%s", postamble))

	})
	ginkgo.It("canonical implement prompt does not make write the literal final action", func() {
		preambleBody, err := os.ReadFile(cliAssetPath("lanes", "implement", "preamble.md"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read implement preamble: %v", err))

		postambleBody, err := os.ReadFile(cliAssetPath("lanes", "implement", "postamble.md"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read implement postamble: %v", err))

		prompt := string(preambleBody) + "\n" + string(postambleBody)
		forbidden := []string{
			"Your final action MUST be a successful write",
			"This dispatch is complete only after an allowed file edit has been attempted",
		}

		for _, phrase := range forbidden {
			gomega.Expect(prompt).NotTo(gomega.ContainSubstring(phrase), fmt.Sprintf("implement prompt retains conflicting closeout phrase %q:\n%s", phrase, prompt))

		}

		for _, want := range []string{
			"before closeout",
			"lane-owned sanity",
			"do not make placeholder edits",
			"runtime/preflight-accepted source-satisfied closeout",
			"source_sanity",
			"`git diff --check` is patch hygiene only",
		} {
			gomega.Expect(prompt).To(gomega.ContainSubstring(want), fmt.Sprintf("implement prompt missing clarified closeout language %q:\n%s", want, prompt))

		}
	})
	ginkgo.It("plan mode prompt does not request write through graph refresh", func() {
		body, err := os.ReadFile(filepath.Join(laneTestRepoRoot(), "prompts", "plan-mode.md"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read plan-mode prompt: %v", err))

		prompt := string(body)
		for _, want := range []string{
			"fresh or report-only context-graph evidence",
			"Do not request write-through graph refresh or index repair in plan mode",
		} {
			gomega.Expect(prompt).To(gomega.ContainSubstring(want), fmt.Sprintf("plan-mode prompt missing non-mutating context guidance %q:\n%s", want, prompt))

		}
		gomega.Expect(prompt).NotTo(gomega.ContainSubstring("Refresh stale graph evidence only"), fmt.Sprintf("plan-mode prompt still asks the model to refresh graph evidence in read-only mode:\n%s", prompt))

	})
	ginkgo.It("canonical plan execute preamble does not repeat source body rule", func() {
		body, err := os.ReadFile(cliAssetPath("lanes", "plan-execute", "preamble.md"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read plan-execute preamble: %v", err))

		prompt := string(body)
		gomega.Expect(prompt).NotTo(gomega.ContainSubstring("Existing required paths still need source-body evidence; this does not waive"), fmt.Sprintf("plan-execute preamble retained duplicate source-body sentence:\n%s", prompt))

		normalized := strings.Join(strings.Fields(prompt), " ")
		for _, want := range []string{
			"Existing required paths still need source-body evidence in this planner turn",
			"this does not waive the source-body obligation above",
			"small insertions/appends",
			"large additions/appends",
			"unchanged line N plus the inserted content",
		} {
			gomega.Expect(normalized).To(gomega.ContainSubstring(want), fmt.Sprintf("plan-execute preamble missing source-body invariant %q:\n%s", want, prompt))

		}
	})
	ginkgo.It("canonical lane prompts keep candidate scope separate from required edit paths", func() {
		paths := []string{
			cliAssetPath("lanes", "plan-execute", "preamble.md"),
			cliAssetPath("lanes", "implement", "preamble.md"),
			cliAssetPath("lanes", "rules", "brief-skeleton.md"),
		}
		for _, path := range paths {
			body, err := os.ReadFile(path)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read %s: %v", path, err))

			text := string(body)
			normalized := strings.Join(strings.Fields(text), " ")

			for _, forbidden := range []string{
				"Files You May Edit`, and near-equivalent allowlist headings as `required_edit_paths`",
				"Files You May Edit\" (and near-equivalents) as `required_edit_paths`",
			} {
				gomega.Expect(normalized).NotTo(gomega.ContainSubstring(strings.Join(strings.Fields(forbidden), " ")), fmt.Sprintf("%s promotes candidate scope to required_edit_paths:\n%s", path, text))

			}
			if strings.Contains(text, "Files You May Edit") {
				gomega.Expect(normalized).To(gomega.ContainSubstring("allowed/candidate scope only"), fmt.Sprintf("%s mentions Files You May Edit without candidate-scope-only wording:\n%s", path, text))
			}

		}
	})
	ginkgo.It("canonical profile seeds stay in sync", func() {
		for _, name := range []string{"coding.md", "claude-qwen-worker.md"} {
			canonical, err := os.ReadFile(filepath.Join(laneTestRepoRoot(), "prompts", "profiles", name))
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read canonical profile %s: %v", name, err))

			seed, err := canonicalprompts.Profile(name)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read embedded canonical profile %s: %v", name, err))
			gomega.Expect(seed).To(gomega.Equal(string(canonical)), fmt.Sprintf("embedded canonical profile %s drifted from prompts/profiles/%s", name, name))

		}
	})
	ginkgo.It("claude qwen profile defers plan execute protocol to runtime brief", func() {
		body, err := os.ReadFile(filepath.Join(laneTestRepoRoot(), "prompts", "profiles", "claude-qwen-worker.md"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read claude-qwen profile: %v", err))

		text := string(body)
		normalizedText := strings.Join(strings.Fields(text), " ")

		for _, want := range []string{
			"The model owns strategy and proposed content",
			"does not define plan-execute planner protocol",
			"compiled into the runtime brief and visible tool schema",
			"runtime brief remains authoritative for lane protocol",
			"follow that brief and the visible tool schema exactly",
			"Hidden reasoning is not a tool call",
		} {
			gomega.Expect(normalizedText).To(gomega.ContainSubstring(strings.Join(strings.Fields(want), " ")), fmt.Sprintf("claude-qwen profile missing runtime-brief ownership wording %q:\n%s", want, text))

		}

		for _, stale := range []string{
			"submit_plan",
			"Edit Shapes (plan-execute)",
			"source-satisfied",
			"Decision:",
			"planner-owned",
			"accepts three edit shapes",
			"line-range first",
		} {
			gomega.Expect(text).NotTo(gomega.ContainSubstring(stale), fmt.Sprintf("claude-qwen profile retained static plan-execute protocol wording %q:\n%s", stale, text))

		}
	})
	ginkgo.It("canonical prompt files do not encourage noop writes", func() {
		paths := []string{
			filepath.Join(laneTestRepoRoot(), "prompts", "base.md"),
			filepath.Join(laneTestRepoRoot(), "prompts", "profiles", "coding.md"),
			filepath.Join(laneTestRepoRoot(), "prompts", "profiles", "claude-qwen-worker.md"),
			cliAssetPath("lanes", "implement", "preamble.md"),
			cliAssetPath("lanes", "plan-execute", "postamble.md"),
		}
		for _, path := range paths {
			body, err := os.ReadFile(path)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read %s: %v", path, err))
			gomega.Expect(string(body)).NotTo(gomega.ContainSubstring("no-op write attempt"), fmt.Sprintf("%s still encourages no-op write attempts:\n%s", path, string(body)))

		}
	})
	ginkgo.It("canonical lane rules do not force question when scoped discovery can refresh anchor", func() {
		body, err := os.ReadFile(cliAssetPath("lanes", "rules", "known-traps-generic.md"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read known traps: %v", err))

		prompt := string(body)
		for _, want := range []string{
			"when the lane contract permits scoped evidence gathering",
			"If the brief gives no scoped path, symbol, section, or other bounded fact",
		} {
			gomega.Expect(prompt).To(gomega.ContainSubstring(want), fmt.Sprintf("known-traps prompt missing scoped-discovery anchor guidance %q:\n%s", want, prompt))

		}

		for _, forbidden := range []string{
			"If the brief gave no literal-anchor evidence at all for",
			"ask for the literal anchor",
		} {
			gomega.Expect(prompt).NotTo(gomega.ContainSubstring(forbidden), fmt.Sprintf("known-traps prompt still forces premature human question %q:\n%s", forbidden, prompt))

		}
	})
	ginkgo.It("canonical refine prompts use conditional output language", func() {
		paths := []struct {
			path string
			want string
		}{
			{cliAssetPath("lanes", "refine", "draft.md"), "Artifact output path"},
			{cliAssetPath("lanes", "refine", "draft-markdown.md"), "Artifact output path"},
			{cliAssetPath("lanes", "refine", "review.md"), "Expected path"},
			{cliAssetPath("lanes", "refine", "patch.md"), "Expected path"},
		}
		for _, tc := range paths {
			body, err := os.ReadFile(tc.path)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read %s: %v", tc.path, err))

			prompt := string(body)
			gomega.Expect(prompt).NotTo(gomega.ContainSubstring("Your only job"), fmt.Sprintf("%s uses unconditional job wording that conflicts with blocker closeout:\n%s", tc.path, prompt))

			for _, want := range []string{tc.want, "`<<QUESTION>>:`"} {
				gomega.Expect(prompt).To(gomega.ContainSubstring(want), fmt.Sprintf("%s missing conditional refine contract language %q:\n%s", tc.path, want, prompt))

			}
		}
	})
	ginkgo.It("canonical refine prompts distinguish textual commands from execution", func() {
		paths := []string{
			cliAssetPath("lanes", "refine", "draft.md"),
			cliAssetPath("lanes", "refine", "draft-markdown.md"),
			cliAssetPath("lanes", "refine", "review.md"),
			cliAssetPath("lanes", "refine", "patch.md"),
		}
		for _, path := range paths {
			body, err := os.ReadFile(path)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read %s: %v", path, err))

			prompt := string(body)
			gomega.Expect(prompt).To(gomega.ContainSubstring("no executed shell commands"), fmt.Sprintf("%s must prohibit execution, not textual acceptance command examples:\n%s", path, prompt))
			gomega.Expect(prompt).To(gomega.ContainSubstring("Textual acceptance commands may appear inside the artifact."), fmt.Sprintf("%s must allow textual acceptance command examples inside artifacts:\n%s", path, prompt))
			gomega.Expect(prompt).NotTo(gomega.ContainSubstring("Contract: final-answer artifact; no tool calls, no file edits, no shell commands."), fmt.Sprintf("%s retains ambiguous shell-command contract:\n%s", path, prompt))

		}
	})
	ginkgo.It("run lane main project overlay wrapped by canonical", func() {
		briefBody := "# atom\n"
		briefPath := writeBrief(briefBody)
		overlayDir := filepath.Join(filepath.Dir(briefPath), ".idiom", "prompts", "lanes", "plan-execute")
		gomega.Expect(os.MkdirAll(overlayDir, 0o755)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(overlayDir, "preamble.md"), []byte("PROJECT_PREAMBLE_OVERLAY"), 0o644)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(overlayDir, "postamble.md"), []byte("PROJECT_POSTAMBLE_OVERLAY"), 0o644)).To(gomega.Succeed())

		var stdout, stderr bytes.Buffer

		fake := &laneFakeRunner{}
		args := []string{"plan-execute", "--project-root", filepath.Dir(briefPath), briefPath}

		if got := runLaneMainTest(args, &stdout, &stderr, fake); got != 0 {
			ginkgo.Fail(fmt.Sprintf("expected exit 0, got %d (stderr=%s)", got, stderr.String()))
		}

		prompt := fake.calledPrompt

		canonicalPreIdx := strings.Index(prompt, "Plan-Then-Execute Lane Brief")
		projectPreIdx := strings.Index(prompt, "PROJECT_PREAMBLE_OVERLAY")
		briefIdx := strings.Index(prompt, briefBody)
		projectPostIdx := strings.Index(prompt, "PROJECT_POSTAMBLE_OVERLAY")
		canonicalPostIdx := strings.Index(prompt, "Plan-Then-Execute Closeout")

		idxs := []int{canonicalPreIdx, projectPreIdx, briefIdx, projectPostIdx, canonicalPostIdx}
		for i, idx := range idxs {
			gomega.Expect(idx < 0).To(gomega.BeFalse(), fmt.Sprintf("missing layer %d in composed prompt; idxs=%v\n--- prompt head ---\n%s", i, idxs, prompt[:min(800, len(prompt))]))

		}

		for i := 1; i < len(idxs); i++ {
			gomega.Expect(idxs[i] < idxs[i-1]).To(gomega.BeFalse(), fmt.Sprintf("layer order violated at %d→%d; idxs=%v", i-1, i, idxs))

		}
	})
	ginkgo.It("run lane main honors idiom config env", func() {
		briefPath := writeBrief("# atom\n")
		ginkgo.GinkgoT().
			Setenv("IDIOM_CONFIG", "/custom/idiom-config.json")

		var stdout, stderr bytes.Buffer

		fake := &laneFakeRunner{}

		if got := runLaneMainTest([]string{"plan-execute", briefPath}, &stdout, &stderr, fake); got != 0 {
			ginkgo.Fail(fmt.Sprintf("expected exit 0, got %d (stderr=%s)", got, stderr.String()))
		}
		gomega.Expect(fake.calledPlanOptions.ConfigPath).To(gomega.Equal("/custom/idiom-config.json"), fmt.Sprintf("IDIOM_CONFIG env must override the default config; opts=%+v", fake.calledPlanOptions))

	})
	ginkgo.It("run lane main config flag beats env", func() {
		briefPath := writeBrief("# atom\n")
		ginkgo.GinkgoT().
			Setenv("IDIOM_CONFIG", "/from/env.json")

		var stdout, stderr bytes.Buffer

		fake := &laneFakeRunner{}
		args := []string{"plan-execute", "--config", "/from/flag.json", briefPath}

		if got := runLaneMainTest(args, &stdout, &stderr, fake); got != 0 {
			ginkgo.Fail(fmt.Sprintf("expected exit 0, got %d (stderr=%s)", got, stderr.String()))
		}
		gomega.Expect(fake.calledPlanOptions.ConfigPath).To(gomega.Equal("/from/flag.json"), fmt.Sprintf("--config must beat IDIOM_CONFIG env; opts=%+v", fake.calledPlanOptions))

	})
	ginkgo.It("run lane main auto routes plan execute to backends executor", func() {
		briefPath := writeBrief("# atom")
		root := filepath.Dir(briefPath)
		gomega.Expect(os.MkdirAll(filepath.Join(root, ".idiom"), 0o755)).To(gomega.Succeed())

		body := `{"profiles": {"local": {"models": [
		{"alias": "tagged-executor", "roles": ["executor"]}
	]}}}`
		gomega.Expect(os.WriteFile(filepath.Join(root, ".idiom", "backends.json"), []byte(body), 0o644)).To(gomega.Succeed())

		var stdout, stderr bytes.Buffer

		fake := &laneFakeRunner{}
		args := []string{"plan-execute", "--project-root", root, briefPath}

		if got := runLaneMainTest(args, &stdout, &stderr, fake); got != 0 {
			ginkgo.Fail(fmt.Sprintf("expected exit 0, got %d (stderr=%s)", got, stderr.String()))
		}
		gomega.Expect(fake.calledPlanOptions.Model).To(gomega.Equal("tagged-executor"), fmt.Sprintf("plan-execute must auto-route to backends.json executor role; opts=%+v", fake.calledPlanOptions))

	})
	ginkgo.It("run lane main honors explicit model override", func() {
		briefPath := writeBrief("# atom")

		var stdout, stderr bytes.Buffer

		fake := &laneFakeRunner{}
		args := []string{"plan-execute", "--model", "qwen3.6-35b", briefPath}

		if got := runLaneMainTest(args, &stdout, &stderr, fake); got != 0 {
			ginkgo.Fail(fmt.Sprintf("expected exit 0, got %d", got))
		}
		gomega.Expect(fake.calledPlanOptions.Model).To(gomega.Equal("qwen3.6-35b"), fmt.Sprintf("--model override must reach plan-execute options; opts=%+v", fake.calledPlanOptions))

	})
	ginkgo.It("run lane main forwards no thinking override", func() {
		briefPath := writeBrief("# atom")

		var stdout, stderr bytes.Buffer

		fake := &laneFakeRunner{}
		args := []string{"implement", "--no-thinking", briefPath}

		if got := runLaneMainTest(args, &stdout, &stderr, fake); got != 0 {
			ginkgo.Fail(fmt.Sprintf("expected exit 0, got %d stderr=%s", got, stderr.String()))
		}
		gomega.Expect(argsContainFlag(fake.calledArgs, "--no-thinking")).To(gomega.BeTrue(), fmt.Sprintf("--no-thinking must reach the underlying subcommand; got: %v", fake.calledArgs))

	})
	ginkgo.It("run lane main implement does not auto route", func() {
		briefPath := writeBrief("# atom")
		root := filepath.Dir(briefPath)
		gomega.Expect(os.MkdirAll(filepath.Join(root, ".idiom"), 0o755)).To(gomega.Succeed())

		body := `{"profiles": {"local": {"models": [
		{"alias": "tagged-executor", "roles": ["executor"]}
	]}}}`
		gomega.Expect(os.WriteFile(filepath.Join(root, ".idiom", "backends.json"), []byte(body), 0o644)).To(gomega.Succeed())

		var stdout, stderr bytes.Buffer

		fake := &laneFakeRunner{}
		args := []string{"implement", "--project-root", root, briefPath}

		if got := runLaneMainTest(args, &stdout, &stderr, fake); got != 0 {
			ginkgo.Fail(fmt.Sprintf("expected exit 0, got %d", got))
		}
		gomega.Expect(argsContain(fake.calledArgs, "--model", "tagged-executor")).To(gomega.BeFalse(), fmt.Sprintf("implement mode must NOT auto-route to the executor role; got: %v", fake.calledArgs))

	})
	ginkgo.It("run lane main coder flag omits model without backends", func() {
		briefPath := writeBrief("# atom")

		var stdout, stderr bytes.Buffer

		fake := &laneFakeRunner{}
		args := []string{"implement", "--coder", briefPath}

		if got := runLaneMainTest(args, &stdout, &stderr, fake); got != 0 {
			ginkgo.Fail(fmt.Sprintf("expected exit 0, got %d", got))
		}

		for i, a := range fake.calledArgs {
			gomega.Expect(a).NotTo(gomega.Equal("--model"), fmt.Sprintf("--coder without backends.json must NOT inject --model; got %v at index %d", fake.calledArgs, i))

		}
	})
	ginkgo.It("run lane main coder flag resolves via backends", func() {
		briefPath := writeBrief("# atom")
		root := filepath.Dir(briefPath)
		gomega.Expect(os.MkdirAll(filepath.Join(root, ".idiom"), 0o755)).To(gomega.Succeed())

		body := `{"profiles": {"local": {"models": [
		{"alias": "project-executor", "roles": ["executor"]}
	]}}}`
		gomega.Expect(os.WriteFile(filepath.Join(root, ".idiom", "backends.json"), []byte(body), 0o644)).To(gomega.Succeed())

		var stdout, stderr bytes.Buffer

		fake := &laneFakeRunner{}
		args := []string{"implement", "--project-root", root, "--coder", briefPath}

		if got := runLaneMainTest(args, &stdout, &stderr, fake); got != 0 {
			ginkgo.Fail(fmt.Sprintf("expected exit 0, got %d (stderr=%s)", got, stderr.String()))
		}
		gomega.Expect(argsContain(fake.calledArgs, "--model", "project-executor")).To(gomega.BeTrue(), fmt.Sprintf("--coder must resolve via backends.json executor role; forwarded: %v", fake.calledArgs))

	})
	ginkgo.It("run lane main emits lane start event", func() {
		briefPath := writeBrief("# atom")

		var stdout, stderr bytes.Buffer

		fake := &laneFakeRunner{}
		args := []string{"plan-execute", "--json", briefPath}

		if got := runLaneMainTest(args, &stdout, &stderr, fake); got != 0 {
			ginkgo.Fail(fmt.Sprintf("expected exit 0, got %d", got))
		}

		out := stdout.String()
		gomega.Expect(out).To(gomega.ContainSubstring(`"type":"`+lanecontract.EventName(lanecontract.EventStart)+`"`), fmt.Sprintf("expected idiom.lane.start event in JSON stream; got: %s", out))
		gomega.Expect(out).To(gomega.ContainSubstring(`"mode":"plan-execute"`), fmt.Sprintf("lane.start event must carry the mode; got: %s", out))

	})
	ginkgo.It("run lane main propagates exit code", func() {
		briefPath := writeBrief("# atom")

		var stdout, stderr bytes.Buffer

		fake := &laneFakeRunner{exitCode: 7}
		got := runLaneMainTest([]string{"plan-execute", briefPath}, &stdout, &stderr, fake)
		gomega.Expect(got).To(gomega.Equal(7), fmt.Sprintf("expected propagated exit code 7, got %d", got))

	})
	ginkgo.It("run lane main resolves project root from config", func() {

		root := ginkgo.GinkgoT().TempDir()
		planningDir := filepath.Join(root, ".planning")
		gomega.Expect(os.MkdirAll(planningDir, 0o755)).To(gomega.Succeed())

		briefPath := filepath.Join(planningDir, "atom.md")
		gomega.Expect(os.WriteFile(briefPath, []byte(minimalValidPlanExecuteBrief()), 0o644)).To(gomega.Succeed())

		overlayDir := filepath.Join(root, ".idiom", "prompts", "lanes", "plan-execute")
		gomega.Expect(os.MkdirAll(overlayDir, 0o755)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(overlayDir, "preamble.md"), []byte("REPO_OVERLAY_SENTINEL"), 0o644)).To(gomega.Succeed())

		cfg := config.Default()

		cfg.Workspace.Root = root
		cfg.Workspace.EnableWrites = true

		var stdout, stderr bytes.Buffer

		fake := &laneFakeRunner{}
		deps := laneDeps{
			loadConfig:       func(string) (config.Config, error) { return cfg, nil },
			runPlanExecute:   fake.plan,
			runImplement:     fake.implement,
			defaultPromptRun: func(config.Config, string, cmdruntime.RunOptions, io.Writer) (string, error) { return "", nil },
		}

		if got := runLaneMainWithDeps([]string{"plan-execute", briefPath}, &stdout, &stderr, deps); got != 0 {
			ginkgo.Fail(fmt.Sprintf("expected exit 0, got %d (stderr=%s)", got, stderr.String()))
		}
		gomega.Expect(fake.calledPrompt).To(gomega.ContainSubstring("REPO_OVERLAY_SENTINEL"), fmt.Sprintf("project overlay must resolve from cfg.Workspace.Root, not the brief's parent directory; got prompt: %s", fake.calledPrompt[:min(400, len(fake.calledPrompt))]))

	})
	ginkgo.It("run lane main project root flag wins over config", func() {
		configRoot := ginkgo.GinkgoT().TempDir()
		flagRoot := ginkgo.GinkgoT().TempDir()

		overlayDir := filepath.Join(flagRoot, ".idiom", "prompts", "lanes", "plan-execute")
		gomega.Expect(os.MkdirAll(overlayDir, 0o755)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(overlayDir, "preamble.md"), []byte("FLAG_ROOT_OVERLAY"), 0o644)).To(gomega.Succeed())

		briefPath := writeBrief(minimalValidPlanExecuteBrief())
		cfg := config.Default()

		cfg.Workspace.Root = configRoot

		var stdout, stderr bytes.Buffer

		fake := &laneFakeRunner{}
		deps := laneDeps{
			loadConfig:       func(string) (config.Config, error) { return cfg, nil },
			runPlanExecute:   fake.plan,
			runImplement:     fake.implement,
			defaultPromptRun: func(config.Config, string, cmdruntime.RunOptions, io.Writer) (string, error) { return "", nil },
		}
		args := []string{"plan-execute", "--project-root", flagRoot, briefPath}

		if got := runLaneMainWithDeps(args, &stdout, &stderr, deps); got != 0 {
			ginkgo.Fail(fmt.Sprintf("expected exit 0, got %d", got))
		}
		gomega.Expect(fake.calledPrompt).To(gomega.ContainSubstring("FLAG_ROOT_OVERLAY"), fmt.Sprintf("--project-root flag must override config workspace.root for overlay lookup; prompt: %s", fake.calledPrompt[:min(400, len(fake.calledPrompt))]))

	})
	ginkgo.It("run lane main coder flag and model flag model wins", func() {
		briefPath := writeBrief("# atom")

		var stdout, stderr bytes.Buffer

		fake := &laneFakeRunner{}
		args := []string{"implement", "--coder", "--model", "qwen3.6-35b", briefPath}

		if got := runLaneMainTest(args, &stdout, &stderr, fake); got != 0 {
			ginkgo.Fail(fmt.Sprintf("expected exit 0, got %d", got))
		}
		gomega.Expect(argsContain(fake.calledArgs, "--model", "qwen3.6-35b")).To(gomega.BeTrue(), fmt.Sprintf("--model must beat --coder when both are set; got: %v", fake.calledArgs))
		gomega.Expect(argsContain(fake.calledArgs, "--model", "qwen3-coder-30b")).To(gomega.BeFalse(), fmt.Sprintf("--model override must NOT leave --coder's qwen3-coder-30b in args: %v", fake.calledArgs))

	})
	ginkgo.It("run lane main resume forwards", func() {
		briefPath := writeBrief("# atom")

		var stdout, stderr bytes.Buffer

		fake := &laneFakeRunner{}
		args := []string{"plan-execute", "--resume", "20260509-180015.118", briefPath}

		if got := runLaneMainTest(args, &stdout, &stderr, fake); got != 0 {
			ginkgo.Fail(fmt.Sprintf("expected exit 0, got %d", got))
		}
		gomega.Expect(fake.calledPlanOptions.Resume).To(gomega.Equal("20260509-180015.118"), fmt.Sprintf("--resume must reach plan-execute options; opts=%+v", fake.calledPlanOptions))

	})
	ginkgo.It("run lane main default does not forward intent loop flags", func() {
		briefPath := writeBrief("# atom")

		var stdout, stderr bytes.Buffer

		fake := &laneFakeRunner{}

		if got := runLaneMainTest([]string{"plan-execute", briefPath}, &stdout, &stderr, fake); got != 0 {
			ginkgo.Fail(fmt.Sprintf("expected exit 0, got %d", got))
		}
		gomega.Expect(fake.calledRunOptions.IntentLoop).To(gomega.BeFalse(), fmt.Sprintf("default plan-execute must not enable intent-loop; run opts=%+v", fake.calledRunOptions))
		gomega.Expect(fake.calledRunOptions.MaxCycles).To(gomega.Equal(5), fmt.Sprintf("default plan-execute must not enable intent-loop; run opts=%+v", fake.calledRunOptions))
		gomega.Expect(fake.calledRunOptions.TotalBudgetTokens).To(gomega.Equal(0), fmt.Sprintf("default plan-execute must not enable intent-loop; run opts=%+v", fake.calledRunOptions))

	})
	ginkgo.It("run lane main intent loop flags forward", func() {
		briefPath := writeBrief("# atom")

		var stdout, stderr bytes.Buffer

		fake := &laneFakeRunner{}
		args := []string{
			"plan-execute",
			"--intent-loop",
			"--max-cycles", "4",
			"--total-budget-tokens", "24000",
			briefPath,
		}

		if got := runLaneMainTest(args, &stdout, &stderr, fake); got != 0 {
			ginkgo.Fail(fmt.Sprintf("expected exit 0, got %d stderr=%s", got, stderr.String()))
		}
		gomega.Expect(fake.calledRunOptions.IntentLoop).To(gomega.BeTrue(), fmt.Sprintf("intent-loop flags must reach typed lane runner options; run opts=%+v", fake.calledRunOptions))
		gomega.Expect(fake.calledRunOptions.MaxCycles).To(gomega.Equal(4), fmt.Sprintf("intent-loop flags must reach typed lane runner options; run opts=%+v", fake.calledRunOptions))
		gomega.Expect(fake.calledRunOptions.TotalBudgetTokens).To(gomega.Equal(24000), fmt.Sprintf("intent-loop flags must reach typed lane runner options; run opts=%+v", fake.calledRunOptions))

	})
	ginkgo.It("run lane main intent loop invalid budget exits2", func() {
		briefPath := writeBrief("# atom")

		var stdout, stderr bytes.Buffer

		fake := &laneFakeRunner{}
		args := []string{"plan-execute", "--intent-loop", "--total-budget-tokens", "-1", briefPath}

		if got := runLaneMainTest(args, &stdout, &stderr, fake); got != 2 {
			ginkgo.Fail(fmt.Sprintf("expected exit 2 for invalid intent-loop budget, got %d", got))
		}
		gomega.Expect(fake.calledMode).To(gomega.Equal(""), fmt.Sprintf("invalid budget must not dispatch subcommand, got mode %q", fake.calledMode))
		gomega.Expect(stderr.String()).To(gomega.ContainSubstring("--total-budget-tokens"), fmt.Sprintf("stderr should name invalid budget flag, got %s", stderr.String()))

	})
	ginkgo.It("run lane main bogus flag exits2", func() {
		briefPath := writeBrief("# atom")

		var stdout, stderr bytes.Buffer

		fake := &laneFakeRunner{}
		args := []string{"plan-execute", "--no-such-flag", briefPath}

		if got := runLaneMainTest(args, &stdout, &stderr, fake); got != 2 {
			ginkgo.Fail(fmt.Sprintf("expected exit 2 for bad flag, got %d (stderr=%s)", got, stderr.String()))
		}
	})
	ginkgo.It("run lane main brief dir rejected", func() {
		dir := ginkgo.GinkgoT().TempDir()

		var stdout, stderr bytes.Buffer

		fake := &laneFakeRunner{}

		if got := runLaneMainTest([]string{"plan-execute", dir}, &stdout, &stderr, fake); got != 2 {
			ginkgo.Fail(fmt.Sprintf("expected exit 2 when brief path is a directory, got %d", got))
		}
		gomega.Expect(stderr.String()).To(gomega.ContainSubstring("directory"), fmt.Sprintf("error must mention directory; got: %s", stderr.String()))

	})
	ginkgo.It("run lane main missing brief rejected", func() {
		var stdout, stderr bytes.Buffer

		fake := &laneFakeRunner{}

		if got := runLaneMainTest([]string{"plan-execute", "/nonexistent/atom.md"}, &stdout, &stderr, fake); got != 2 {
			ginkgo.Fail(fmt.Sprintf("expected exit 2 for missing brief, got %d", got))
		}
		gomega.Expect(stderr.String()).To(gomega.Or(
			gomega.ContainSubstring("not readable"),
			gomega.ContainSubstring("no such"),
		), fmt.Sprintf("error must indicate brief unreadable; got: %s", stderr.String()))

	})
	ginkgo.It("run lane main embeds canonical rules", func() {
		briefPath := writeBrief("# atom\n")

		var stdout, stderr bytes.Buffer

		fake := &laneFakeRunner{}

		if got := runLaneMainTest([]string{"plan-execute", briefPath}, &stdout, &stderr, fake); got != 0 {
			ginkgo.Fail(fmt.Sprintf("expected exit 0, got %d (stderr=%s)", got, stderr.String()))
		}

		prompt := fake.calledPrompt
		required := []string{
			"# Loop Protocol",
			"# Brief Skeleton",
			"# Known Traps — Generic",
		}

		for _, marker := range required {
			gomega.Expect(prompt).To(gomega.ContainSubstring(marker), fmt.Sprintf("composed prompt missing canonical rule header %q; head=%s", marker, prompt[:min(800, len(prompt))]))

		}
	})
	ginkgo.It("run lane main canonical rules precede preamble", func() {
		briefPath := writeBrief("# atom\n")

		var stdout, stderr bytes.Buffer

		fake := &laneFakeRunner{}

		if got := runLaneMainTest([]string{"plan-execute", briefPath}, &stdout, &stderr, fake); got != 0 {
			ginkgo.Fail(fmt.Sprintf("expected exit 0, got %d", got))
		}

		prompt := fake.calledPrompt
		rulesIdx := strings.Index(prompt, "# Loop Protocol")
		preambleIdx := strings.Index(prompt, "Plan-Then-Execute Lane Brief")
		gomega.Expect(rulesIdx).NotTo(gomega.BeNumerically("<", 0), fmt.Sprintf("missing layer marker; rules=%d preamble=%d", rulesIdx, preambleIdx))
		gomega.Expect(preambleIdx).NotTo(gomega.BeNumerically("<", 0), fmt.Sprintf("missing layer marker; rules=%d preamble=%d", rulesIdx, preambleIdx))
		gomega.Expect(rulesIdx >= preambleIdx).To(gomega.BeFalse(), fmt.Sprintf("rules block must precede canonical preamble; rules@%d preamble@%d", rulesIdx, preambleIdx))

	})
	ginkgo.It("run lane main project rule replaces canonical", func() {
		briefPath := writeBrief("# atom\n")
		root := filepath.Dir(briefPath)
		rulesDir := filepath.Join(root, ".idiom", "prompts", "rules")
		gomega.Expect(os.MkdirAll(rulesDir, 0o755)).To(gomega.Succeed())

		override := "PROJECT_LOOP_PROTOCOL_REPLACEMENT — project supplies its own loop rules"
		gomega.Expect(os.WriteFile(filepath.Join(rulesDir, "loop-protocol.md"), []byte(override), 0o644)).To(gomega.Succeed())

		var stdout, stderr bytes.Buffer

		fake := &laneFakeRunner{}
		args := []string{"plan-execute", "--project-root", root, briefPath}

		if got := runLaneMainTest(args, &stdout, &stderr, fake); got != 0 {
			ginkgo.Fail(fmt.Sprintf("expected exit 0, got %d (stderr=%s)", got, stderr.String()))
		}

		prompt := fake.calledPrompt
		gomega.Expect(prompt).To(gomega.ContainSubstring("PROJECT_LOOP_PROTOCOL_REPLACEMENT"), fmt.Sprintf("project rule replacement must appear in composed prompt; head=%s", prompt[:min(800, len(prompt))]))
		gomega.
			Expect(prompt).NotTo(gomega.ContainSubstring("# Loop Protocol"), fmt.Sprintf("project replacement must displace canonical loop-protocol.md; canonical header still present"))
		gomega.Expect(prompt).To(gomega.ContainSubstring("# Brief Skeleton"), fmt.Sprintf("only the same-named canonical rule should be replaced; other canonical rules went missing"))
		gomega.Expect(prompt).To(gomega.ContainSubstring("# Known Traps"), fmt.Sprintf("only the same-named canonical rule should be replaced; other canonical rules went missing"))

	})
	ginkgo.It("run lane main project rule extras append", func() {
		briefPath := writeBrief("# atom\n")
		root := filepath.Dir(briefPath)
		rulesDir := filepath.Join(root, ".idiom", "prompts", "rules")
		gomega.Expect(os.MkdirAll(rulesDir, 0o755)).To(gomega.Succeed())
		gomega.Expect(

			os.WriteFile(filepath.Join(rulesDir, "z-late-rule.md"), []byte("EXTRA_Z_SENTINEL"), 0o644)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(rulesDir, "a-early-rule.md"), []byte("EXTRA_A_SENTINEL"), 0o644)).To(gomega.Succeed())

		var stdout, stderr bytes.Buffer

		fake := &laneFakeRunner{}
		args := []string{"plan-execute", "--project-root", root, briefPath}

		if got := runLaneMainTest(args, &stdout, &stderr, fake); got != 0 {
			ginkgo.Fail(fmt.Sprintf("expected exit 0, got %d (stderr=%s)", got, stderr.String()))
		}

		prompt := fake.calledPrompt

		canonicalLastIdx := strings.Index(prompt, "# Known Traps — Generic")
		extraAIdx := strings.Index(prompt, "EXTRA_A_SENTINEL")
		extraZIdx := strings.Index(prompt, "EXTRA_Z_SENTINEL")
		gomega.Expect(canonicalLastIdx).NotTo(gomega.BeNumerically("<", 0), fmt.Sprintf("missing layer marker; canonical=%d a=%d z=%d", canonicalLastIdx, extraAIdx, extraZIdx))
		gomega.Expect(extraAIdx).NotTo(gomega.BeNumerically("<", 0), fmt.Sprintf("missing layer marker; canonical=%d a=%d z=%d", canonicalLastIdx, extraAIdx, extraZIdx))
		gomega.Expect(extraZIdx).NotTo(gomega.BeNumerically("<", 0), fmt.Sprintf("missing layer marker; canonical=%d a=%d z=%d", canonicalLastIdx, extraAIdx, extraZIdx))
		gomega.Expect(canonicalLastIdx).To(gomega.BeNumerically("<", extraAIdx), fmt.Sprintf("extras must follow canonical block in alphabetical order; canonical@%d a@%d z@%d", canonicalLastIdx, extraAIdx, extraZIdx))
		gomega.Expect(extraAIdx).To(gomega.BeNumerically("<", extraZIdx), fmt.Sprintf("extras must follow canonical block in alphabetical order; canonical@%d a@%d z@%d", canonicalLastIdx, extraAIdx, extraZIdx))

	})
	ginkgo.It("run lane main empty project rules dir graceful", func() {
		briefPath := writeBrief("# atom\n")
		root := filepath.Dir(briefPath)
		gomega.Expect(os.MkdirAll(filepath.Join(root, ".idiom", "prompts", "rules"), 0o755)).To(gomega.Succeed())

		var stdout, stderr bytes.Buffer

		fake := &laneFakeRunner{}
		args := []string{"plan-execute", "--project-root", root, briefPath}

		if got := runLaneMainTest(args, &stdout, &stderr, fake); got != 0 {
			ginkgo.Fail(fmt.Sprintf("expected exit 0, got %d (stderr=%s)", got, stderr.String()))
		}
		gomega.Expect(fake.calledPrompt).To(gomega.ContainSubstring("# Loop Protocol"), fmt.Sprintf("canonical rules must still load when the project rules dir is empty"))

	})
})

func (f *laneFakeRunner) plan(_ config.Config, opts planexecute.Options, runOpts planexecuterunner.Options, _ planexecute.PromptRunner, _ *telemetry.Recorder, stdout io.Writer, _ io.Writer) planexecute.RunResult {
	f.calledMode = "plan-execute"

	f.calledPlanOptions = opts
	f.calledRunOptions = runOpts
	f.calledPrompt = opts.Prompt
	if f.calledPrompt == "" {
		f.calledPrompt = readPromptFromFile(opts.PromptFile)
	}

	emitFakeEditAttempt(stdout, "submit_plan")

	return planexecute.RunResult{ExitCode: f.exitCode}
}

func (f *laneFakeRunner) implement(args []string, stdout, _ io.Writer, _ cmdruntime.PromptRunner) int {
	f.calledMode = "implement"

	f.calledArgs = append([]string(nil), args...)
	f.calledPrompt = readPromptFromArgs(args)

	emitFakeEditAttempt(stdout, "edit_file")

	return f.exitCode
}

func emitFakeEditAttempt(stdout io.Writer, tool string) {
	if stdout == nil {
		return
	}

	_, _ = stdout.Write([]byte(`{"type":"tool_call","tool":"` + tool + `"}` + "\n"))
}

func readPromptFromArgs(args []string) string {
	for i, a := range args {
		if a == "--prompt-file" && i+1 < len(args) {
			return readPromptFromFile(args[i+1])
		}
	}

	return ""
}

func readPromptFromFile(path string) string {
	body, err := os.ReadFile(path)
	if err != nil {
		return ""
	}

	return string(body)
}

func writeBrief(body string) string {

	dir := ginkgo.GinkgoT().TempDir()
	path := filepath.Join(dir, "brief.md")
	gomega.Expect(os.WriteFile(path, []byte(body), 0o644)).To(gomega.Succeed())

	return path
}

func minimalValidPlanExecuteBrief() string {
	return "# atom\n\n" +
		"## Files To Modify\n\n" +
		"| File | Action |\n" +
		"| --- | --- |\n" +
		"| `.planning/trace.md` | Create this new file. |\n\n" +
		"## Verified Current State\n\n" +
		"The planning note does not exist yet.\n\n" +
		"## Required Edits\n\n" +
		"Create `.planning/trace.md` with a short trace note.\n\n" +
		"## Acceptance\n\n" +
		"- Lane wrapper dispatch reaches the fake runner.\n"
}

func argsContain(args []string, flag, value string) bool {
	for i, a := range args {
		if a == flag && i+1 < len(args) && args[i+1] == value {
			return true
		}
	}

	return false
}

func argsContainFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}

	return false
}

func runLaneMainTest(args []string, stdout, stderr io.Writer, fake *laneFakeRunner) int {

	stubLaneBriefLint(passingLaneBriefLint)

	cfg := config.Default()

	cfg.Workspace.EnableWrites = true

	deps := laneDeps{
		loadConfig:       func(string) (config.Config, error) { return cfg, nil },
		runPlanExecute:   fake.plan,
		runImplement:     fake.implement,
		defaultPromptRun: func(config.Config, string, cmdruntime.RunOptions, io.Writer) (string, error) { return "", nil },
	}

	return runLaneMainWithDeps(args, stdout, stderr, deps)
}

func stubLaneBriefLint(stub func(context.Context, string, string, time.Duration, io.Writer, io.Writer) (brieflint.TextRun, error)) {

	orig := laneBriefLintRunner

	laneBriefLintRunner = stub
	ginkgo.DeferCleanup(func() { laneBriefLintRunner = orig })
}

func passingLaneBriefLint(context.Context, string, string, time.Duration, io.Writer, io.Writer) (brieflint.TextRun, error) {
	return brieflint.TextRun{Exit: 0}, nil
}

func laneBriefLintStub(run func(path string) int) func(context.Context, string, string, time.Duration, io.Writer, io.Writer) (brieflint.TextRun, error) {
	return func(_ context.Context, path string, _ string, _ time.Duration, _ io.Writer, _ io.Writer) (brieflint.TextRun, error) {
		return brieflint.TextRun{Exit: run(path)}, nil
	}
}

func stubLanePostflightRunner(stub func(args []string, stdout, stderr io.Writer) int) {

	orig := lanePostflightRunner

	lanePostflightRunner = stub
	ginkgo.DeferCleanup(func() { lanePostflightRunner = orig })
}

func intFromTelemetry(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case float64:
		return int(typed)
	default:
		return 0
	}
}

func findLaneTelemetryRecord(records []map[string]any, eventType string) map[string]any {
	for _, record := range records {
		if record["type"] == eventType {
			return record
		}
	}

	return nil
}
