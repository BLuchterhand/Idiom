package lane

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/evidence/telemetry"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/agent"
	canonicalprompts "idiom/prompts"
)

// laneFakeRunner captures the arguments handed to the underlying
// idiom subcommand so tests can assert on what the wrapper forwarded.
type laneFakeRunner struct {
	calledMode   string
	calledArgs   []string
	calledPrompt string
	exitCode     int
}

func TestLaneBriefIntakeProjectionRequiresTypedDispatchableScopeForLegacyHeadings(t *testing.T) {
	body := "# Atom\n\n## Required Edits\n\n- Change the banner text.\n"
	projection := laneBriefIntakeProjection(body)

	if !projection.NeedsAtomIntake || projection.Dispatchable || projection.State != "terminal" || projection.Reason != "missing_scope" {
		t.Fatalf("heading-only atom brief should trigger non-dispatchable typed atom intake without scope: %#v", projection)
	}
}

func TestLaneBriefIntakeProjectionAllowsLegacyHeadingsAfterScope(t *testing.T) {
	body := "# Atom\n\n## Files To Modify\n\n- pkg/feature.go\n\n## Required Edits\n\n- Change the banner text.\n"
	projection := laneBriefIntakeProjection(body)

	if !projection.NeedsAtomIntake || !projection.Dispatchable || !projection.LegacyHeadingFallback ||
		!strings.HasPrefix(projection.Reason, "legacy_heading:") {

		t.Fatalf("scoped legacy heading brief should trigger atom intake through compatibility fallback: %#v", projection)
	}
}

func (f *laneFakeRunner) plan(args []string, stdout, _ io.Writer, _ promptRunner) int {
	f.calledMode = "plan-execute"

	f.calledArgs = append([]string(nil), args...)
	f.calledPrompt = readPromptFromArgs(args)

	emitFakeEditAttempt(stdout, "submit_plan")

	return f.exitCode
}

func (f *laneFakeRunner) implement(args []string, stdout, _ io.Writer, _ promptRunner) int {
	f.calledMode = "implement"

	f.calledArgs = append([]string(nil), args...)
	f.calledPrompt = readPromptFromArgs(args)

	emitFakeEditAttempt(stdout, "edit_file")

	return f.exitCode
}

// emitFakeEditAttempt writes a minimal synthetic tool-call event to
// stdout so the lane wrapper's Phase 6.5 post-analyzer sees a
// satisfied edit-attempt contract. Without this, every test that
// dispatches through laneFakeRunner would trigger the
// `no_edit_attempt` degradation since the fake produces no real
// tool calls. Tests that specifically want to exercise the
// degradation path bypass this helper by using runLaneMainWithCustomRunners.
func emitFakeEditAttempt(stdout io.Writer, tool string) {
	if stdout == nil {
		return
	}

	_, _ = stdout.Write([]byte(`{"type":"tool_call","tool":"` + tool + `"}` + "\n"))
}

func readPromptFromArgs(args []string) string {
	for i, a := range args {
		if a == "--prompt-file" && i+1 < len(args) {
			body, err := os.ReadFile(args[i+1])
			if err != nil {
				return ""
			}

			return string(body)
		}
	}

	return ""
}

func writeBrief(t *testing.T, body string) string {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "brief.md")

	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write brief: %v", err)
	}

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

// TestRunLaneMainRequiresBrief verifies the wrapper rejects calls
// with no positional brief, mirroring run_idiom_impl.sh's behavior.
func TestRunLaneMainRequiresBrief(t *testing.T) {
	var stdout, stderr bytes.Buffer

	fake := &laneFakeRunner{}
	code := runLaneMainTest(t, []string{"plan-execute"}, &stdout, &stderr, fake)

	if code == 0 {
		t.Fatalf("expected non-zero exit for missing brief, got 0")
	}

	if !strings.Contains(stderr.String(), "brief") {
		t.Fatalf("error should mention the missing brief, got: %s", stderr.String())
	}
}

// TestRunLaneMainRejectsUnknownMode verifies the wrapper validates
// the mode argument with a clear error.
func TestRunLaneMainRejectsUnknownMode(t *testing.T) {
	briefPath := writeBrief(t, "# Test\nDo a thing.\n")

	var stdout, stderr bytes.Buffer

	fake := &laneFakeRunner{}
	code := runLaneMainTest(t, []string{"bogus-mode", briefPath}, &stdout, &stderr, fake)

	if code != 2 {
		t.Fatalf("expected exit 2 for unknown mode, got %d", code)
	}

	out := stderr.String()
	if !strings.Contains(out, "plan-execute") || !strings.Contains(out, "implement") {
		t.Fatalf("error should list valid modes, got: %s", out)
	}
}

// TestRunLaneMainPlanExecuteForwards verifies that a plan-execute
// invocation reaches the plan-execute subcommand entry point.
func TestRunLaneMainPlanExecuteForwards(t *testing.T) {
	briefPath := writeBrief(t, "# Plan-execute brief\nFiles To Modify: x.txt")

	var stdout, stderr bytes.Buffer

	fake := &laneFakeRunner{}

	if got := runLaneMainTest(t, []string{"plan-execute", briefPath}, &stdout, &stderr, fake); got != 0 {
		t.Fatalf("expected exit 0, got %d (stderr=%s)", got, stderr.String())
	}

	if fake.calledMode != "plan-execute" {
		t.Fatalf("expected forwarding to plan-execute, got %q", fake.calledMode)
	}
}

func TestRunLaneMainPlanExecuteForwardsRequiredEditPathsFromBrief(t *testing.T) {
	briefPath := writeBrief(t, "# Generic brief\n\n"+
		"## Files to Modify\n\n"+
		"Exhaustive allowlist for this run:\n\n"+
		"| Path | Reason |\n"+
		"|---|---|\n"+
		"| `src/foo.go` | Update behavior. |\n"+
		"| `.planning/tracker.md` | Record accepted evidence. |\n")
	root := filepath.Dir(briefPath)

	var stdout, stderr bytes.Buffer

	fake := &laneFakeRunner{}

	code := runLaneMainTest(t, []string{"plan-execute", "--project-root", root, briefPath}, &stdout, &stderr, fake)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}

	got := flagValues(fake.calledArgs, "--required-edit-path")
	want := []string{"src/foo.go", ".planning/tracker.md"}

	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("forwarded required edit paths = %#v, want %#v; args=%v", got, want, fake.calledArgs)
	}
}

func TestRunLaneMainPlanExecuteForwardsAtomIntakeScopePaths(t *testing.T) {
	root := t.TempDir()
	briefPath := filepath.Join(root, "atom.md")
	absFooPath := filepath.Join(root, "src", "foo.go")
	brief := "# Atom\n\n" +
		"Status: pending\n\n" +
		"## Scope\n\n" +
		"- `" + absFooPath + "`\n" +
		"- `.planning/tracker.md`\n\n" +
		"## Acceptance\n\n" +
		"- The lane reaches the fake runner.\n"

	if err := os.WriteFile(briefPath, []byte(brief), 0o644); err != nil {
		t.Fatalf("write brief: %v", err)
	}

	var stdout, stderr bytes.Buffer

	fake := &laneFakeRunner{}

	code := runLaneMainTest(t, []string{"plan-execute", "--json", "--project-root", root, briefPath}, &stdout, &stderr, fake)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}

	got := flagValues(fake.calledArgs, "--required-edit-path")
	want := []string{"src/foo.go", ".planning/tracker.md"}

	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("forwarded required edit paths = %#v, want %#v; args=%v", got, want, fake.calledArgs)
	}

	if !strings.Contains(stdout.String(), `"state":"dispatchable"`) {
		t.Fatalf("atom intake should report dispatchable scope, stdout=%s", stdout.String())
	}
}

func TestRunLaneMainAtomIntakeBlocksNonDispatchableBeforeRunner(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "foo.go"), []byte("package main\n\nfunc done() {}\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	briefPath := filepath.Join(root, "atom.md")
	brief := "# Atom\n\n## Files To Modify\n\n| File | Change |\n|---|---|\n| `foo.go` | Add done helper. |\n\n## Required Edits\n\nIn `foo.go`, add:\n\n```go\nfunc done() {}\n```\n"

	if err := os.WriteFile(briefPath, []byte(brief), 0o644); err != nil {
		t.Fatalf("write brief: %v", err)
	}

	var stdout, stderr bytes.Buffer

	fake := &laneFakeRunner{}
	code := runLaneMainTest(t, []string{"plan-execute", "--json", "--project-root", root, briefPath}, &stdout, &stderr, fake)

	if code == 0 {
		t.Fatalf("expected atom intake to block dispatch")
	}

	if fake.calledMode != "" {
		t.Fatalf("fake runner should not be called, got mode %q", fake.calledMode)
	}

	if !strings.Contains(stderr.String(), "atom intake blocked dispatch") || !strings.Contains(stderr.String(), "source-satisfied") {
		t.Fatalf("stderr should explain intake block, got:\n%s", stderr.String())
	}

	if !strings.Contains(stdout.String(), `"type":"idiom.lane.atom_intake"`) || !strings.Contains(stdout.String(), `"dispatchable":false`) {
		t.Fatalf("stdout should include atom intake event, got:\n%s", stdout.String())
	}
}

func TestRunLaneMainAtomIntakeOwnsMissingScopeBeforeRunner(t *testing.T) {
	root := t.TempDir()
	briefPath := filepath.Join(root, "atom.md")
	brief := "# Atom\n\n## Required Edits\n\n- Change the banner text.\n"

	if err := os.WriteFile(briefPath, []byte(brief), 0o644); err != nil {
		t.Fatalf("write brief: %v", err)
	}

	var stdout, stderr bytes.Buffer

	fake := &laneFakeRunner{}
	code := runLaneMainTest(t, []string{"plan-execute", "--json", "--project-root", root, briefPath}, &stdout, &stderr, fake)

	if code == 0 {
		t.Fatalf("expected missing-scope atom intake to block dispatch")
	}
	if fake.calledMode != "" {
		t.Fatalf("fake runner should not be called, got mode %q", fake.calledMode)
	}
	for _, want := range []string{
		`"type":"idiom.lane.atom_intake"`,
		`"state":"missing-scope"`,
		`"workflow_id":"atom_intake"`,
		`"workflow_state":"terminal"`,
		`"workflow_reason":"missing_scope"`,
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout missing %s:\n%s", want, stdout.String())
		}
	}
	if !strings.Contains(stderr.String(), "atom intake blocked dispatch") {
		t.Fatalf("stderr should explain intake block, got:\n%s", stderr.String())
	}
}

func TestRunLaneMainAtomIntakeRecognizesImplementationSnippetWithoutStatus(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "foo.go"), []byte("package main\n\nfunc done() {}\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	briefPath := filepath.Join(root, "atom.md")
	brief := "# Atom\n\n## Files To Modify\n\n| File | Change |\n|---|---|\n| `foo.go` | Add done helper. |\n\n## Implementation\n\nIn `foo.go`, insert:\n\n```go\nfunc done() {}\n```\n"

	if err := os.WriteFile(briefPath, []byte(brief), 0o644); err != nil {
		t.Fatalf("write brief: %v", err)
	}

	var stdout, stderr bytes.Buffer

	fake := &laneFakeRunner{}
	code := runLaneMainTest(t, []string{"plan-execute", "--json", "--project-root", root, briefPath}, &stdout, &stderr, fake)

	if code == 0 {
		t.Fatalf("expected implementation snippet intake to block dispatch")
	}

	if fake.calledMode != "" {
		t.Fatalf("fake runner should not be called, got mode %q", fake.calledMode)
	}

	if !strings.Contains(stderr.String(), "source-satisfied") {
		t.Fatalf("stderr should explain source-satisfied implementation snippet, got:\n%s", stderr.String())
	}
}

func TestRunLaneMainAtomIntakeBlocksDirtyScopedPathBeforeRunner(t *testing.T) {
	root := initGitRepoForStaleness(t)
	gitCommit(t, root, "src/foo.go", "package src\n", "initial")

	if err := os.WriteFile(filepath.Join(root, "src/foo.go"), []byte("package src\n\nfunc dirty() {}\n"), 0o644); err != nil {
		t.Fatalf("dirty source: %v", err)
	}

	brief := "# Atom\n\nStatus: planned\n\n## Files To Modify\n\n| File | Change |\n|---|---|\n| `src/foo.go` | Modify source. |\n"
	briefPath := writeBriefFile(t, root, "atom.md", brief, time.Now().Add(time.Hour))

	var stdout, stderr bytes.Buffer

	fake := &laneFakeRunner{}
	code := runLaneMainTest(t, []string{"plan-execute", "--json", "--project-root", root, briefPath}, &stdout, &stderr, fake)

	if code == 0 {
		t.Fatalf("expected dirty atom scope to block dispatch")
	}

	if fake.calledMode != "" {
		t.Fatalf("fake runner should not be called, got mode %q", fake.calledMode)
	}

	if !strings.Contains(stderr.String(), "blocked-dirty-files") || !strings.Contains(stderr.String(), "src/foo.go") {
		t.Fatalf("stderr should explain dirty scope block, got:\n%s", stderr.String())
	}

	if !strings.Contains(stdout.String(), `"state":"blocked-dirty-files"`) || !strings.Contains(stdout.String(), `"type":"idiom.lane.run_ledger"`) {
		t.Fatalf("stdout should include intake and ledger events, got:\n%s", stdout.String())
	}
}

func TestRunLaneMainAtomIntakeAllowDirtyDispatches(t *testing.T) {
	root := initGitRepoForStaleness(t)
	gitCommit(t, root, "src/foo.go", "package src\n", "initial")

	if err := os.WriteFile(filepath.Join(root, "src/foo.go"), []byte("package src\n\nfunc dirty() {}\n"), 0o644); err != nil {
		t.Fatalf("dirty source: %v", err)
	}

	brief := "# Atom\n\nStatus: planned\n\n## Files To Modify\n\n| File | Change |\n|---|---|\n| `src/foo.go` | Modify source. |\n"
	briefPath := writeBriefFile(t, root, "atom.md", brief, time.Now().Add(time.Hour))

	for _, flag := range []string{"--allow-dirty", "--allow-dirty-scope"} {
		t.Run(flag, func(t *testing.T) {
			var stdout, stderr bytes.Buffer

			fake := &laneFakeRunner{}
			code := runLaneMainTest(t, []string{"plan-execute", "--json", flag, "--project-root", root, briefPath}, &stdout, &stderr, fake)

			if code != 0 {
				t.Fatalf("expected %s to dispatch, got %d stderr=%s stdout=%s", flag, code, stderr.String(), stdout.String())
			}

			if fake.calledMode != "plan-execute" {
				t.Fatalf("fake runner should be called, got mode %q", fake.calledMode)
			}

			if !strings.Contains(stdout.String(), `"state":"dispatchable"`) {
				t.Fatalf("stdout should include dispatchable atom intake event, got:\n%s", stdout.String())
			}
		})
	}
}

// TestRunLaneMainImplementForwards mirrors the previous test for
// implement mode.
func TestRunLaneMainImplementForwards(t *testing.T) {
	briefPath := writeBrief(t, "# Implement brief\n")

	var stdout, stderr bytes.Buffer

	fake := &laneFakeRunner{}

	if got := runLaneMainTest(t, []string{"implement", briefPath}, &stdout, &stderr, fake); got != 0 {
		t.Fatalf("expected exit 0, got %d", got)
	}

	if fake.calledMode != "implement" {
		t.Fatalf("expected forwarding to implement, got %q", fake.calledMode)
	}
}

// TestRunLaneMainComposesPreamblePostamble verifies the composed
// prompt the wrapper hands to the underlying subcommand contains the
// brief body wrapped in a preamble + postamble. Project-side overlay
// files are consulted under .idiom/prompts/lanes/<mode>/ when present.
func TestRunLaneMainComposesPreamblePostamble(t *testing.T) {
	briefBody := "# Donkey atom\nFiles To Modify: animal_types.h\n"
	briefPath := writeBrief(t, briefBody)

	var stdout, stderr bytes.Buffer

	fake := &laneFakeRunner{}

	if got := runLaneMainTest(t, []string{"plan-execute", briefPath}, &stdout, &stderr, fake); got != 0 {
		t.Fatalf("expected exit 0, got %d (stderr=%s)", got, stderr.String())
	}

	prompt := fake.calledPrompt
	if !strings.Contains(prompt, briefBody) {
		t.Fatalf("composed prompt must include the brief body verbatim, got: %s", prompt[:min(200, len(prompt))])
	}
}

func TestRunLaneMainInjectsAutomaticContextPackBeforeBrief(t *testing.T) {
	briefBody := "# Routing atom\n\nFiles To Modify:\n- internal/surfaces/tui/app/prompt_routing.go\n"
	briefPath := writeBrief(t, briefBody)

	var stdout, stderr bytes.Buffer

	fake := &laneFakeRunner{}
	cfg := config.Default()

	cfg.Workspace.Root = filepath.Dir(briefPath)
	cfg.Workspace.EnableWrites = true

	stubLaneBriefLint(t, func([]string, io.Writer, io.Writer) int { return 0 })

	deps := laneDeps{
		loadConfig:     func(string) (config.Config, error) { return cfg, nil },
		runPlanExecute: fake.plan,
		runImplement:   fake.implement,
		defaultPromptRun: func(config.Config, string, runOptions, io.Writer) (string, error) {
			return "", nil
		},
		buildContextPack: func(opts laneOptions, got config.Config, stderr io.Writer) string {
			if opts.Mode != "implement" {
				t.Fatalf("context pack saw mode %q, want implement", opts.Mode)
			}

			if got.Workspace.Root != cfg.Workspace.Root {
				t.Fatalf("context pack saw root %q, want %q", got.Workspace.Root, cfg.Workspace.Root)
			}

			return "## Context Pack\n\n- `internal/surfaces/tui/app/prompt_routing.go` (editable): matched task terms\n"
		},
	}
	code := runLaneMainWithDeps([]string{"implement", "--project-root", cfg.Workspace.Root, briefPath}, &stdout, &stderr, deps)

	if code != 0 {
		t.Fatalf("expected exit 0, got %d (stderr=%s)", code, stderr.String())
	}

	prompt := fake.calledPrompt
	contextIdx := strings.Index(prompt, "## Context Pack")
	briefIdx := strings.Index(prompt, briefBody)

	if contextIdx < 0 {
		t.Fatalf("composed prompt missing context pack:\n%s", prompt)
	}

	if briefIdx < 0 {
		t.Fatalf("composed prompt missing brief body:\n%s", prompt)
	}

	if contextIdx > briefIdx {
		t.Fatalf("context pack should precede brief body")
	}

	if !strings.Contains(prompt, briefBodyStartMarker+"\n"+briefBody) {
		t.Fatalf("brief body should remain marker-scoped after context pack injection")
	}
}

func TestRenderLaneContextPackSectionAddsFallbackWhenNoTargets(t *testing.T) {
	section, err := renderLaneContextPackSection(workspace.ToolResult{
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

func TestRenderLaneContextDiscoveryFallbackSectionIncludesObjective(t *testing.T) {
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
		if !strings.Contains(section, want) {
			t.Fatalf("fallback section missing %q:\n%s", want, section)
		}
	}

	for _, notWant := range []string{"before writing", "before the first write"} {
		if strings.Contains(section, notWant) {
			t.Fatalf("fallback prompt should not use implementation-only phrase %q:\n%s", notWant, section)
		}
	}
}

func TestRunLaneMainTelemetryMatchesDoctorReadinessProfile(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("IDIOM_RUNNER_SOCKET", filepath.Join(root, "missing-runner.sock"))

	configPath := filepath.Join(root, ".idiom", "config.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}

	configBody := `{
  "workspace": {"root": "` + root + `", "enable_writes": true},
  "llama_server": {"model": "coder", "kind": "mlx", "port": 47781, "ctx_size": 8192},
  "chat": {"max_tokens": 2048},
  "route_classifier": {"enabled": false}
}`
	if err := os.WriteFile(configPath, []byte(configBody), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	stageDoctorBackends(t, root)

	briefPath := filepath.Join(root, "brief.md")
	if err := os.WriteFile(briefPath, []byte("# Implement brief\n\nNo prompt secret should appear in telemetry.\n"), 0o644); err != nil {
		t.Fatalf("write brief: %v", err)
	}

	rec, err := telemetry.Open(telemetry.Options{
		Dir:           filepath.Join(root, "telemetry"),
		Command:       commandTelemetryName([]string{"lane", "implement", "--config", configPath, "--prompt", "SECRET raw prompt body"}),
		Args:          []string{"lane", "implement", "--config", configPath, "--prompt", "SECRET raw prompt body"},
		WorkspaceRoot: root,
	})
	if err != nil {
		t.Fatalf("telemetry.Open: %v", err)
	}

	var stdout, stderr bytes.Buffer

	fake := &laneFakeRunner{}

	stubLaneBriefLint(t, func([]string, io.Writer, io.Writer) int { return 0 })

	deps := laneDeps{
		loadConfig:     config.Load,
		runPlanExecute: fake.plan,
		runImplement:   fake.implement,
		defaultPromptRun: func(config.Config, string, runOptions, io.Writer) (string, error) {
			return "", nil
		},
		buildContextPack: func(laneOptions, config.Config, io.Writer) string { return "" },
		telemetry:        rec,
	}
	code := runLaneMainWithDeps([]string{"implement", "--json", "--no-context-pack", "--config", configPath, "--project-root", root, briefPath}, &stdout, &stderr, deps)

	if code != 0 {
		t.Fatalf("expected exit 0, got %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}

	if !strings.Contains(stdout.String(), `"type":"idiom.lane.writer_route_decision"`) ||
		!strings.Contains(stdout.String(), `"writer_mode":"agentic_guarded"`) ||
		!strings.Contains(stdout.String(), `"role_aliases"`) {

		t.Fatalf("lane JSON should report writer route decision with role aliases, got %s", stdout.String())
	}

	if err := rec.Close(0); err != nil {
		t.Fatalf("rec.Close: %v", err)
	}

	doctorProfiles, err := buildDoctorReadinessProfiles(configPath, "implement", nil)
	if err != nil {
		t.Fatalf("build doctor readiness: %v", err)
	}

	doctor := doctorProfiles[0]
	records := readMainTelemetryRecords(t, rec.Path())
	session := findLaneTelemetryRecord(records, "session_start")

	if session == nil || session["command"] != "lane:implement" {
		t.Fatalf("session_start should identify lane implement command, got %#v", session)
	}

	start := findLaneTelemetryRecord(records, "lane_start")
	if start == nil {
		t.Fatalf("missing lane_start telemetry in %#v", records)
	}

	if start["readiness_profile"] != doctor.Name ||
		start["readiness_state"] != doctor.State ||
		start["backend_state"] != doctor.Metrics.BackendState ||
		start["queue_state"] != doctor.Metrics.QueueState ||
		start["context_budget_state"] != doctor.Metrics.ContextBudgetState ||
		start["route_classifier_state"] != doctor.Metrics.ClassifierState ||
		intFromTelemetry(start["hard_failures"]) != doctor.Metrics.HardFailures ||
		intFromTelemetry(start["soft_degradations"]) != doctor.Metrics.SoftDegradations {

		t.Fatalf("lane_start telemetry should match doctor readiness metrics\nlane=%#v\ndoctor=%#v", start, doctor)
	}

	if start["enforcement"] != "report_only" {
		t.Fatalf("P6.4 must not escalate readiness enforcement, got %#v", start)
	}

	aliases, ok := start["role_aliases"].(map[string]any)
	if !ok || aliases["executor"] != doctor.RoleAliases["executor"] {
		t.Fatalf("lane_start should carry doctor-compatible role aliases, got %#v doctor=%#v", start["role_aliases"], doctor.RoleAliases)
	}

	localOps, ok := start["local_ops"].(map[string]any)
	if !ok ||
		localOps["context_budget_state"] != doctor.Metrics.ContextBudgetState ||
		localOps["route_classifier_state"] != doctor.Metrics.ClassifierState ||
		localOps["backend_state"] != doctor.Metrics.BackendState {

		t.Fatalf("local_ops should expose doctor-compatible state names where practical, got %#v doctor=%#v", localOps, doctor.Metrics)
	}

	rendered, _ := json.Marshal(records)
	for _, forbidden := range []string{"SECRET raw prompt body", "finance", "healthcare/life sciences", "public-sector", "enterprise SaaS", "tooling-vendor", "local-model/private-code", "open-source"} {
		if strings.Contains(string(rendered), forbidden) {
			t.Fatalf("lane telemetry should remain metadata-only and client-neutral; found %q in %s", forbidden, rendered)
		}
	}
}

func TestRunLaneMainPlanExecuteJSONEmitsWriterRouteDecision(t *testing.T) {
	root := t.TempDir()
	briefPath := filepath.Join(root, "brief.md")

	if err := os.WriteFile(briefPath, []byte("# Plan execute brief\n\nUpdate the route decision tests.\n"), 0o644); err != nil {
		t.Fatalf("write brief: %v", err)
	}

	var stdout, stderr bytes.Buffer

	fake := &laneFakeRunner{}
	code := runLaneMainTest(t, []string{"plan-execute", "--json", "--project-root", root, briefPath}, &stdout, &stderr, fake)

	if code != 0 {
		t.Fatalf("expected exit 0, got %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}

	events := planExecuteJSONEvents(t, stdout.String())
	event, ok := planExecuteEventByType(events, "idiom.lane.writer_route_decision")

	if !ok {
		t.Fatalf("missing lane writer route event in %s", stdout.String())
	}

	if event["writer_mode"] != "plan_submission" || event["lane"] != "plan-execute" || event["verification_owner"] != "planner_owned" {
		t.Fatalf("unexpected plan-execute lane route event: %#v", event)
	}

	if event["task_family"] != agent.PlanThenExecutePlannerTaskFamily {
		t.Fatalf("plan-execute lane route event should carry task_family, got %#v", event)
	}

	if !jsonEventStringSliceContains(event["reason_codes"], "route.multi_file_plan_execute") {
		t.Fatalf("plan-execute lane route should explain multi-file plan-execute dispatch: %#v", event)
	}
}

func TestRunLaneMainNoContextPackSkipsAutomaticContextPack(t *testing.T) {
	briefPath := writeBrief(t, "# Routing atom\n\nFiles To Modify:\n- internal/surfaces/tui/app/prompt_routing.go\n")

	var stdout, stderr bytes.Buffer

	fake := &laneFakeRunner{}
	cfg := config.Default()

	stubLaneBriefLint(t, func([]string, io.Writer, io.Writer) int { return 0 })

	calls := 0
	deps := laneDeps{
		loadConfig:       func(string) (config.Config, error) { return cfg, nil },
		runPlanExecute:   fake.plan,
		runImplement:     fake.implement,
		defaultPromptRun: func(config.Config, string, runOptions, io.Writer) (string, error) { return "", nil },
		buildContextPack: func(laneOptions, config.Config, io.Writer) string {
			calls++
			return "## Context Pack\n\nshould not appear\n"
		},
	}
	code := runLaneMainWithDeps([]string{"implement", "--no-context-pack", briefPath}, &stdout, &stderr, deps)

	if code != 0 {
		t.Fatalf("expected exit 0, got %d (stderr=%s)", code, stderr.String())
	}

	if calls != 0 {
		t.Fatalf("context pack builder should not run when --no-context-pack is set, got %d call(s)", calls)
	}

	if strings.Contains(fake.calledPrompt, "should not appear") {
		t.Fatalf("context pack section leaked into prompt")
	}
}

func TestRunLaneMainSkipsContextPackForRefineScratchpadMetaBrief(t *testing.T) {
	root := t.TempDir()
	metaDir := filepath.Join(root, ".idiom", "refine", "Atom", "iter-1")

	if err := os.MkdirAll(metaDir, 0o755); err != nil {
		t.Fatal(err)
	}

	briefPath := filepath.Join(metaDir, "meta-patch.md")
	if err := os.WriteFile(briefPath, []byte("# META - Patch brief\n\nFiles To Modify:\n- .idiom/refine/Atom/iter-2/draft.md\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer

	fake := &laneFakeRunner{}
	cfg := config.Default()

	cfg.Workspace.Root = root

	stubLaneBriefLint(t, func([]string, io.Writer, io.Writer) int { return 0 })

	calls := 0
	deps := laneDeps{
		loadConfig:       func(string) (config.Config, error) { return cfg, nil },
		runPlanExecute:   fake.plan,
		runImplement:     fake.implement,
		defaultPromptRun: func(config.Config, string, runOptions, io.Writer) (string, error) { return "", nil },
		buildContextPack: func(laneOptions, config.Config, io.Writer) string {
			calls++
			return "## Context Pack\n\nshould not appear\n"
		},
	}
	code := runLaneMainWithDeps([]string{"implement", "--project-root", root, briefPath}, &stdout, &stderr, deps)

	if code != 0 {
		t.Fatalf("expected exit 0, got %d (stderr=%s)", code, stderr.String())
	}

	if calls != 0 {
		t.Fatalf("context pack builder should not run for refine scratchpad meta briefs, got %d call(s)", calls)
	}

	if strings.Contains(fake.calledPrompt, "should not appear") {
		t.Fatalf("context pack section leaked into prompt")
	}
}

func TestRenderLaneContextPackSectionOmitsCacheArtifactPath(t *testing.T) {
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
	if err != nil {
		t.Fatalf("render context pack: %v", err)
	}

	if strings.Contains(section, ".idiom/comprehension/views/abc123.json") {
		t.Fatalf("context pack section should not expose cache artifact as a workspace evidence path:\n%s", section)
	}

	for _, want := range []string{"context view artifact", "`internal/surfaces/tui/app/prompt_routing.go`", "`runtimeBriefForRoute`", "`go test ./internal/surfaces/tui/app`"} {
		if !strings.Contains(section, want) {
			t.Fatalf("expected %q in context section:\n%s", want, section)
		}
	}
}

func TestBuildLaneContextPackSectionSeedsRequiredEditPaths(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/lanecontext\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(filepath.Join(root, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(root, "pkg", "target.go"), []byte("package pkg\n\nfunc Target() string { return \"target\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(root, "pkg", "noisy.go"), []byte("package pkg\n\nfunc Noisy() string { return \"noisy\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	mgr := workspace.NewManager(config.Workspace{Root: root, CWD: root})
	if result := mgr.Execute(context.Background(), workspace.ToolRequest{Tool: "context_index", Language: "go"}); result.Error != "" {
		t.Fatalf("context_index returned error: %v\n%s", result.Error, result.Output)
	}

	brief := "# Lane atom\n\n## Task\n\nPlan a change to Noisy.\n\n## Files To Modify\n\n| File | Change |\n|---|---|\n| `pkg/target.go` | edit target |\n"
	briefPath := filepath.Join(root, "atom.md")

	if err := os.WriteFile(briefPath, []byte(brief), 0o644); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer

	section := buildLaneContextPackSection(laneOptions{
		Mode:        "plan-execute",
		BriefPath:   briefPath,
		ProjectRoot: root,
		Title:       "Lane atom",
	}, config.Config{Workspace: config.Workspace{Root: root, CWD: root}}, &stderr)

	if !strings.Contains(section, "`pkg/target.go`") {
		t.Fatalf("automatic context pack should include Files To Modify path, stderr=%s\nsection:\n%s", stderr.String(), section)
	}
}

// TestRunLaneMainComposesProjectOverlay verifies that a project's
// .idiom/prompts/lanes/<mode>/preamble.md is appended to the composed
// prompt before the brief body. Project overlay must not replace
// idiom's preamble — it extends it (concatenation merge).
func TestRunLaneMainComposesProjectOverlay(t *testing.T) {
	briefBody := "# atom\nFiles: x"
	briefPath := writeBrief(t, briefBody)
	projectRoot := filepath.Dir(filepath.Dir(briefPath)) // brief is in tempdir/brief.md
	overlayDir := filepath.Join(filepath.Dir(briefPath), ".idiom", "prompts", "lanes", "plan-execute")

	if err := os.MkdirAll(overlayDir, 0o755); err != nil {
		t.Fatalf("mkdir overlay: %v", err)
	}

	overlayBody := "PROJECT_OVERLAY_SENTINEL: Colony-specific guidance"
	if err := os.WriteFile(filepath.Join(overlayDir, "preamble.md"), []byte(overlayBody), 0o644); err != nil {
		t.Fatalf("write overlay: %v", err)
	}

	var stdout, stderr bytes.Buffer

	fake := &laneFakeRunner{}
	args := []string{"plan-execute", "--project-root", filepath.Dir(briefPath), briefPath}

	_ = projectRoot

	if got := runLaneMainTest(t, args, &stdout, &stderr, fake); got != 0 {
		t.Fatalf("expected exit 0, got %d (stderr=%s)", got, stderr.String())
	}

	prompt := fake.calledPrompt
	if !strings.Contains(prompt, "PROJECT_OVERLAY_SENTINEL") {
		t.Fatalf("composed prompt must include project overlay text; got: %s", prompt[:min(400, len(prompt))])
	}

	if strings.Index(prompt, "PROJECT_OVERLAY_SENTINEL") > strings.Index(prompt, briefBody) {
		t.Fatalf("project overlay must precede the brief body in the composed prompt")
	}
}

// TestRunLaneMainPlanExecuteHonorsBackendsJSONCoder verifies the
// Phase 4 contract: when a project's .idiom/backends.json declares
// proxy.default_coder, the plan-execute auto-route uses that alias
// instead of the hardcoded fallback. This is what retires the
// `// TODO(phase-4):` markers from the previous phase.
func TestRunLaneMainPlanExecuteHonorsBackendsJSONCoder(t *testing.T) {
	briefPath := writeBrief(t, "# atom\n")
	root := filepath.Dir(briefPath)

	if err := os.MkdirAll(filepath.Join(root, ".idiom"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	body := `{"profiles": {"local": {"models": [{"alias": "custom-coder-7b"}], "proxy": {"default_coder": "custom-coder-7b"}}}}`
	if err := os.WriteFile(filepath.Join(root, ".idiom", "backends.json"), []byte(body), 0o644); err != nil {
		t.Fatalf("write backends.json: %v", err)
	}

	var stdout, stderr bytes.Buffer

	fake := &laneFakeRunner{}
	args := []string{"plan-execute", "--project-root", root, briefPath}

	if got := runLaneMainTest(t, args, &stdout, &stderr, fake); got != 0 {
		t.Fatalf("expected exit 0, got %d (stderr=%s)", got, stderr.String())
	}

	if !argsContain(fake.calledArgs, "--model", "custom-coder-7b") {
		t.Fatalf("plan-execute auto-route must use backends.json default_coder; forwarded: %v", fake.calledArgs)
	}
}

// TestRunLaneMainCoderShortcutHonorsBackendsJSON verifies the same
// resolution path for the explicit `--coder` flag. The flag and the
// auto-route share a single resolver, so this prevents a regression
// where one path picks up the project alias and the other doesn't.
func TestRunLaneMainCoderShortcutHonorsBackendsJSON(t *testing.T) {
	briefPath := writeBrief(t, "# atom\n")
	root := filepath.Dir(briefPath)

	if err := os.MkdirAll(filepath.Join(root, ".idiom"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	body := `{"profiles": {"local": {"models": [{"alias": "fancy-coder-13b"}], "proxy": {"default_coder": "fancy-coder-13b"}}}}`
	if err := os.WriteFile(filepath.Join(root, ".idiom", "backends.json"), []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	var stdout, stderr bytes.Buffer

	fake := &laneFakeRunner{}
	// `--coder` on implement mode: doesn't auto-route, but the flag
	// itself must resolve via backends.json.
	args := []string{"implement", "--project-root", root, "--coder", briefPath}
	if got := runLaneMainTest(t, args, &stdout, &stderr, fake); got != 0 {
		t.Fatalf("expected exit 0, got %d (stderr=%s)", got, stderr.String())
	}

	if !argsContain(fake.calledArgs, "--model", "fancy-coder-13b") {
		t.Fatalf("--coder must resolve via backends.json; forwarded: %v", fake.calledArgs)
	}
}

// TestRunLaneMainOmitsModelFlagWhenNoBackendsJSON verifies the
// single-model contract: a project with no .idiom/backends.json runs
// the lane on whatever cfg.LlamaServer.Model says, with NO forced
// fallback alias. The `--model` flag is omitted from the forwarded
// args so the subcommand reads the configured model from cfg.
//
// (This replaces the earlier TestRunLaneMainFallsBackToHardcodedCoder
// behavior, which baked in `qwen3-coder-30b` and prevented other
// projects from running idiom on a single configured model.)
func TestRunLaneMainOmitsModelFlagWhenNoBackendsJSON(t *testing.T) {
	briefPath := writeBrief(t, "# atom\n")

	var stdout, stderr bytes.Buffer

	fake := &laneFakeRunner{}

	if got := runLaneMainTest(t, []string{"plan-execute", briefPath}, &stdout, &stderr, fake); got != 0 {
		t.Fatalf("expected exit 0, got %d", got)
	}

	for i, a := range fake.calledArgs {
		if a == "--model" {
			t.Fatalf("absent backends.json, plan-execute must NOT inject a --model flag; got %v at index %d", fake.calledArgs, i)
		}
	}
}

// TestRunLaneMainBackendProfileFlagSelectsProfile verifies projects
// with multiple profiles can route a lane invocation through a
// named one via `--backend-profile`. Without this flag the resolver
// would refuse to pick (ambiguous), so the flag is the disambig.
func TestRunLaneMainBackendProfileFlagSelectsProfile(t *testing.T) {
	briefPath := writeBrief(t, "# atom\n")
	root := filepath.Dir(briefPath)

	if err := os.MkdirAll(filepath.Join(root, ".idiom"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	body := `{"profiles": {
		"prod": {"models": [{"alias": "prod-coder"}], "proxy": {"default_coder": "prod-coder"}},
		"dev":  {"models": [{"alias": "dev-coder"}],  "proxy": {"default_coder": "dev-coder"}}
	}}`
	if err := os.WriteFile(filepath.Join(root, ".idiom", "backends.json"), []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	var stdout, stderr bytes.Buffer

	fake := &laneFakeRunner{}
	args := []string{"plan-execute", "--project-root", root, "--backend-profile", "dev", briefPath}

	if got := runLaneMainTest(t, args, &stdout, &stderr, fake); got != 0 {
		t.Fatalf("expected exit 0, got %d (stderr=%s)", got, stderr.String())
	}

	if !argsContain(fake.calledArgs, "--model", "dev-coder") {
		t.Fatalf("--backend-profile dev must resolve to dev-coder; forwarded: %v", fake.calledArgs)
	}
}

func TestRunLaneMainBackendProfilePlannerSelectsPlanExecutePrimary(t *testing.T) {
	briefPath := writeBrief(t, "# atom\n")
	root := filepath.Dir(briefPath)

	if err := os.MkdirAll(filepath.Join(root, ".idiom"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	body := `{"profiles": {
		"qwen-27b-pair": {"models": [
			{"alias": "qwen3.6-27b", "roles": ["planner"]},
			{"alias": "qwen3-coder-30b", "roles": ["executor"]}
		], "proxy": {"default_planner": "qwen3.6-27b", "default_executor": "qwen3-coder-30b"}}
	}}`
	if err := os.WriteFile(filepath.Join(root, ".idiom", "backends.json"), []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	var stdout, stderr bytes.Buffer

	fake := &laneFakeRunner{}
	args := []string{"plan-execute", "--project-root", root, "--backend-profile", "qwen-27b-pair", briefPath}

	if got := runLaneMainTest(t, args, &stdout, &stderr, fake); got != 0 {
		t.Fatalf("expected exit 0, got %d (stderr=%s)", got, stderr.String())
	}

	if !argsContain(fake.calledArgs, "--model", "qwen3.6-27b") {
		t.Fatalf("--backend-profile qwen-27b-pair must resolve plan-execute to planner; forwarded: %v", fake.calledArgs)
	}
}

func TestRunLaneMainExplicitCoderUsesExecutorWithPlannerProfile(t *testing.T) {
	briefPath := writeBrief(t, "# atom\n")
	root := filepath.Dir(briefPath)

	if err := os.MkdirAll(filepath.Join(root, ".idiom"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	body := `{"profiles": {
		"qwen-27b-pair": {"models": [
			{"alias": "qwen3.6-27b", "roles": ["planner"]},
			{"alias": "qwen3-coder-30b", "roles": ["executor"]}
		], "proxy": {"default_planner": "qwen3.6-27b", "default_executor": "qwen3-coder-30b"}}
	}}`
	if err := os.WriteFile(filepath.Join(root, ".idiom", "backends.json"), []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	var stdout, stderr bytes.Buffer

	fake := &laneFakeRunner{}
	args := []string{"plan-execute", "--project-root", root, "--backend-profile", "qwen-27b-pair", "--coder", briefPath}

	if got := runLaneMainTest(t, args, &stdout, &stderr, fake); got != 0 {
		t.Fatalf("expected exit 0, got %d (stderr=%s)", got, stderr.String())
	}

	if !argsContain(fake.calledArgs, "--model", "qwen3-coder-30b") {
		t.Fatalf("--coder must keep using the executor role; forwarded: %v", fake.calledArgs)
	}
}

// TestRunLaneMainExplicitModelBeatsBackendsJSON verifies the
// precedence ladder: an explicit --model wins over both the
// auto-route and the backends.json default_coder. Operators
// debugging a model-specific issue need to be able to override the
// project default without editing backends.json.
func TestRunLaneMainExplicitModelBeatsBackendsJSON(t *testing.T) {
	briefPath := writeBrief(t, "# atom\n")
	root := filepath.Dir(briefPath)

	if err := os.MkdirAll(filepath.Join(root, ".idiom"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	body := `{"profiles": {"local": {"proxy": {"default_coder": "project-default-coder"}}}}`
	if err := os.WriteFile(filepath.Join(root, ".idiom", "backends.json"), []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	var stdout, stderr bytes.Buffer

	fake := &laneFakeRunner{}
	args := []string{"plan-execute", "--project-root", root, "--model", "operator-override", briefPath}

	if got := runLaneMainTest(t, args, &stdout, &stderr, fake); got != 0 {
		t.Fatalf("expected exit 0, got %d", got)
	}

	if !argsContain(fake.calledArgs, "--model", "operator-override") {
		t.Fatalf("--model must beat backends.json default_coder; forwarded: %v", fake.calledArgs)
	}
}

// TestRunLaneMainEmbedsCanonicalPlanExecutePreamble verifies the
// idiom-canonical plan-execute preamble (lifted from Colony's
// run_idiom_impl.sh) is embedded into the binary at build time and
// prepended to every plan-execute composed prompt. The "PLANNER" /
// "submit_plan" framing is the load-bearing instruction for the
// plan-execute lane — losing it converts the lane into a write-mode
// run that the harness will then reject.
func TestRunLaneMainEmbedsCanonicalPlanExecutePreamble(t *testing.T) {
	briefBody := "# atom\nFiles To Modify: x\n"
	briefPath := writeBrief(t, briefBody)

	var stdout, stderr bytes.Buffer

	fake := &laneFakeRunner{}

	if got := runLaneMainTest(t, []string{"plan-execute", briefPath}, &stdout, &stderr, fake); got != 0 {
		t.Fatalf("expected exit 0, got %d (stderr=%s)", got, stderr.String())
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
		if !strings.Contains(normalizedPrompt, strings.Join(strings.Fields(marker), " ")) {
			t.Fatalf("plan-execute composed prompt missing canonical preamble marker %q; head=%s", marker, prompt[:min(400, len(prompt))])
		}
	}

	for _, forbidden := range []string{
		"sufficient source evidence for the plan",
		"Exact anchors, current-state anchors, full new-file bodies, explicit replacement edit shapes, unified patches, and ranked packet evidence are sufficient planning evidence",
		"if the location is known from the packet, plan it directly",
	} {
		if strings.Contains(normalizedPrompt, strings.Join(strings.Fields(forbidden), " ")) {
			t.Fatalf("plan-execute composed prompt retained ambiguous source-evidence phrase %q:\n%s", forbidden, prompt)
		}
	}

	if strings.Index(prompt, "Plan-Then-Execute Lane Brief") > strings.Index(prompt, briefBody) {
		t.Fatalf("canonical preamble must precede the brief body")
	}
}

// TestRunLaneMainEmbedsCanonicalImplementPreamble mirrors the
// plan-execute test for the implement lane: the embedded preamble
// must surface so the model sees idiom's write-capable contract
// language (allowlist behavior, named_gap budget, harness_feedback
// expectations) without relying on a project-side copy. It also
// verifies the submit_plan exclusion language is present — implement
// mode should not invoke the plan-execute planner-phase write tool,
// and the reciprocal preamble guidance closes the asymmetry that
// previously only the plan-execute side spelled out.
func TestRunLaneMainEmbedsCanonicalImplementPreamble(t *testing.T) {
	briefBody := "# atom\nFiles To Modify: x\n"
	briefPath := writeBrief(t, briefBody)

	var stdout, stderr bytes.Buffer

	fake := &laneFakeRunner{}

	if got := runLaneMainTest(t, []string{"implement", briefPath}, &stdout, &stderr, fake); got != 0 {
		t.Fatalf("expected exit 0, got %d (stderr=%s)", got, stderr.String())
	}

	prompt := fake.calledPrompt
	required := []string{
		"Implementation Lane Brief",
		"write-capable implementation contract",
		"named_gap",
		// submit_plan exclusion language — Phase 3 review #2 closeout.
		"Do NOT call",
		"submit_plan",
	}

	for _, marker := range required {
		if !strings.Contains(prompt, marker) {
			t.Fatalf("implement composed prompt missing canonical preamble marker %q; head=%s", marker, prompt[:min(400, len(prompt))])
		}
	}
}

func TestCanonicalImplementPreambleUsesCapabilityLookupLanguage(t *testing.T) {
	body, err := os.ReadFile(cliAssetPath("lanes", "implement", "preamble.md"))
	if err != nil {
		t.Fatalf("read implement preamble: %v", err)
	}

	preamble := string(body)
	normalized := strings.Join(strings.Fields(preamble), " ")

	for _, want := range []string{
		"source sanity",
		"symbol navigation or body inspection",
		"reference/callsite evidence",
		"project-declared read-only evidence",
		"exact text/range confirmation",
	} {
		if !strings.Contains(normalized, want) {
			t.Fatalf("implement preamble missing capability-oriented lookup language %q:\n%s", want, preamble)
		}
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
		if strings.Contains(preamble, forbidden) {
			t.Fatalf("implement preamble still contains raw lookup strategy menu term %q:\n%s", forbidden, preamble)
		}
	}
}

// TestRunLaneMainEmbedsCanonicalPostamble verifies the closeout
// postamble (which tells the model what "done" looks like for each
// lane) is embedded and appended after the brief / project overlay.
func TestRunLaneMainEmbedsCanonicalPostamble(t *testing.T) {
	cases := []struct {
		mode   string
		marker string
	}{
		{"plan-execute", "Plan-Then-Execute Closeout"},
		{"implement", "Implementation Closeout"},
	}
	for _, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			briefPath := writeBrief(t, "# atom\n")

			var stdout, stderr bytes.Buffer

			fake := &laneFakeRunner{}

			if got := runLaneMainTest(t, []string{tc.mode, briefPath}, &stdout, &stderr, fake); got != 0 {
				t.Fatalf("expected exit 0, got %d", got)
			}

			prompt := fake.calledPrompt
			if !strings.Contains(prompt, tc.marker) {
				t.Fatalf("composed prompt missing canonical %s postamble marker %q", tc.mode, tc.marker)
			}

			// Postamble must follow the brief — closeout language ahead of
			// the brief would mislead the model about what's expected.
			briefIdx := strings.Index(prompt, "# atom")
			postIdx := strings.Index(prompt, tc.marker)

			if briefIdx < 0 || postIdx < 0 || postIdx < briefIdx {
				t.Fatalf("canonical postamble must follow brief body; brief@%d post@%d", briefIdx, postIdx)
			}
		})
	}
}

func TestCanonicalPlanExecutePostambleAllowsSourceSatisfiedCloseout(t *testing.T) {
	body, err := os.ReadFile(cliAssetPath("lanes", "plan-execute", "postamble.md"))
	if err != nil {
		t.Fatalf("read plan-execute postamble: %v", err)
	}

	postamble := string(body)
	for _, want := range []string{
		"`submit_plan` has been called successfully",
		"`Decision: source-satisfied`",
		"every required edit path",
		"`<<QUESTION>>:` blocker",
		"Do not submit verification",
		"placeholder edits",
	} {
		if !strings.Contains(postamble, want) {
			t.Fatalf("plan-execute postamble missing terminal contract language %q:\n%s", want, postamble)
		}
	}

	if strings.Contains(postamble, "complete only after `submit_plan`") {
		t.Fatalf("plan-execute postamble must not exclude source-satisfied as a terminal outcome:\n%s", postamble)
	}
}

func TestCanonicalImplementPromptDoesNotMakeWriteTheLiteralFinalAction(t *testing.T) {
	preambleBody, err := os.ReadFile(cliAssetPath("lanes", "implement", "preamble.md"))
	if err != nil {
		t.Fatalf("read implement preamble: %v", err)
	}

	postambleBody, err := os.ReadFile(cliAssetPath("lanes", "implement", "postamble.md"))
	if err != nil {
		t.Fatalf("read implement postamble: %v", err)
	}

	prompt := string(preambleBody) + "\n" + string(postambleBody)
	forbidden := []string{
		"Your final action MUST be a successful write",
		"This dispatch is complete only after an allowed file edit has been attempted",
	}

	for _, phrase := range forbidden {
		if strings.Contains(prompt, phrase) {
			t.Fatalf("implement prompt retains conflicting closeout phrase %q:\n%s", phrase, prompt)
		}
	}

	for _, want := range []string{
		"before closeout",
		"lane-owned sanity",
		"do not make placeholder edits",
		"runtime/preflight-accepted source-satisfied closeout",
		"source_sanity",
		"`git diff --check` is patch hygiene only",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("implement prompt missing clarified closeout language %q:\n%s", want, prompt)
		}
	}
}

func TestPlanModePromptDoesNotRequestWriteThroughGraphRefresh(t *testing.T) {
	body, err := os.ReadFile(filepath.Join(laneTestRepoRoot(t), "prompts", "plan-mode.md"))
	if err != nil {
		t.Fatalf("read plan-mode prompt: %v", err)
	}

	prompt := string(body)
	for _, want := range []string{
		"fresh or report-only context-graph evidence",
		"Do not request write-through graph refresh or index repair in plan mode",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("plan-mode prompt missing non-mutating context guidance %q:\n%s", want, prompt)
		}
	}

	if strings.Contains(prompt, "Refresh stale graph evidence only") {
		t.Fatalf("plan-mode prompt still asks the model to refresh graph evidence in read-only mode:\n%s", prompt)
	}
}

func TestCanonicalPlanExecutePreambleDoesNotRepeatSourceBodyRule(t *testing.T) {
	body, err := os.ReadFile(cliAssetPath("lanes", "plan-execute", "preamble.md"))
	if err != nil {
		t.Fatalf("read plan-execute preamble: %v", err)
	}

	prompt := string(body)
	if strings.Contains(prompt, "Existing required paths still need source-body evidence; this does not waive") {
		t.Fatalf("plan-execute preamble retained duplicate source-body sentence:\n%s", prompt)
	}

	normalized := strings.Join(strings.Fields(prompt), " ")
	for _, want := range []string{
		"Existing required paths still need source-body evidence in this planner turn",
		"this does not waive the source-body obligation above",
		"small insertions/appends",
		"large additions/appends",
		"unchanged line N plus the inserted content",
	} {
		if !strings.Contains(normalized, want) {
			t.Fatalf("plan-execute preamble missing source-body invariant %q:\n%s", want, prompt)
		}
	}
}

func TestCanonicalLanePromptsKeepCandidateScopeSeparateFromRequiredEditPaths(t *testing.T) {
	paths := []string{
		cliAssetPath("lanes", "plan-execute", "preamble.md"),
		cliAssetPath("lanes", "implement", "preamble.md"),
		cliAssetPath("lanes", "rules", "brief-skeleton.md"),
	}
	for _, path := range paths {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}

		text := string(body)
		normalized := strings.Join(strings.Fields(text), " ")

		for _, forbidden := range []string{
			"Files You May Edit`, and near-equivalent allowlist headings as `required_edit_paths`",
			"Files You May Edit\" (and near-equivalents) as `required_edit_paths`",
		} {
			if strings.Contains(normalized, strings.Join(strings.Fields(forbidden), " ")) {
				t.Fatalf("%s promotes candidate scope to required_edit_paths:\n%s", path, text)
			}
		}

		if strings.Contains(text, "Files You May Edit") && !strings.Contains(normalized, "allowed/candidate scope only") {
			t.Fatalf("%s mentions Files You May Edit without candidate-scope-only wording:\n%s", path, text)
		}
	}
}

func TestCanonicalProfileSeedsStayInSync(t *testing.T) {
	for _, name := range []string{"coding.md", "claude-qwen-worker.md"} {
		canonical, err := os.ReadFile(filepath.Join(laneTestRepoRoot(t), "prompts", "profiles", name))
		if err != nil {
			t.Fatalf("read canonical profile %s: %v", name, err)
		}

		seed, err := canonicalprompts.Profile(name)
		if err != nil {
			t.Fatalf("read embedded canonical profile %s: %v", name, err)
		}

		if seed != string(canonical) {
			t.Fatalf("embedded canonical profile %s drifted from prompts/profiles/%s", name, name)
		}
	}
}

func TestClaudeQwenProfileCarriesPlanExecuteSubmitPlanQualityBar(t *testing.T) {
	body, err := os.ReadFile(filepath.Join(laneTestRepoRoot(t), "prompts", "profiles", "claude-qwen-worker.md"))
	if err != nil {
		t.Fatalf("read claude-qwen profile: %v", err)
	}

	text := string(body)
	normalizedText := strings.Join(strings.Fields(text), " ")

	for _, want := range []string{
		"The model owns strategy and proposed content",
		"Hidden reasoning is not a tool call",
		"insert_after_line",
		"Use `old_text` + `new_text` only for a tiny exact snippet",
		"Every edit body must be",
		"executable final content",
		"quoted string containing",
		"Line-range `content` is",
		"replacement for the inclusive range",
	} {
		if !strings.Contains(normalizedText, strings.Join(strings.Fields(want), " ")) {
			t.Fatalf("claude-qwen profile missing submit_plan quality bar %q:\n%s", want, text)
		}
	}

	if !strings.Contains(text, "**Line-range**") || !strings.Contains(text, "**Substring**") {
		t.Fatalf("claude-qwen profile missing edit-shape headings:\n%s", text)
	}

	if !strings.Contains(text, "**Insertion**") || strings.Index(text, "**Insertion**") > strings.Index(text, "**Line-range**") {
		t.Fatalf("claude-qwen profile should present typed insertion before replacement shapes:\n%s", text)
	}

	for _, stale := range []string{"accepts three edit shapes", "line-range first"} {
		if strings.Contains(text, stale) {
			t.Fatalf("claude-qwen profile retained stale submit_plan wording %q:\n%s", stale, text)
		}
	}
}

func TestCanonicalPromptFilesDoNotEncourageNoopWrites(t *testing.T) {
	paths := []string{
		filepath.Join(laneTestRepoRoot(t), "prompts", "base.md"),
		filepath.Join(laneTestRepoRoot(t), "prompts", "profiles", "coding.md"),
		filepath.Join(laneTestRepoRoot(t), "prompts", "profiles", "claude-qwen-worker.md"),
		cliAssetPath("lanes", "implement", "preamble.md"),
		cliAssetPath("lanes", "plan-execute", "postamble.md"),
	}
	for _, path := range paths {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}

		if strings.Contains(string(body), "no-op write attempt") {
			t.Fatalf("%s still encourages no-op write attempts:\n%s", path, string(body))
		}
	}
}

func TestCanonicalLaneRulesDoNotForceQuestionWhenScopedDiscoveryCanRefreshAnchor(t *testing.T) {
	body, err := os.ReadFile(cliAssetPath("lanes", "rules", "known-traps-generic.md"))
	if err != nil {
		t.Fatalf("read known traps: %v", err)
	}

	prompt := string(body)
	for _, want := range []string{
		"when the lane contract permits scoped evidence gathering",
		"If the brief gives no scoped path, symbol, section, or other bounded fact",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("known-traps prompt missing scoped-discovery anchor guidance %q:\n%s", want, prompt)
		}
	}

	for _, forbidden := range []string{
		"If the brief gave no literal-anchor evidence at all for",
		"ask for the literal anchor",
	} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("known-traps prompt still forces premature human question %q:\n%s", forbidden, prompt)
		}
	}
}

func TestCanonicalRefinePromptsUseConditionalOutputLanguage(t *testing.T) {
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
		if err != nil {
			t.Fatalf("read %s: %v", tc.path, err)
		}

		prompt := string(body)
		if strings.Contains(prompt, "Your only job") {
			t.Fatalf("%s uses unconditional job wording that conflicts with blocker closeout:\n%s", tc.path, prompt)
		}

		for _, want := range []string{tc.want, "`<<QUESTION>>:`"} {
			if !strings.Contains(prompt, want) {
				t.Fatalf("%s missing conditional refine contract language %q:\n%s", tc.path, want, prompt)
			}
		}
	}
}

func TestCanonicalRefinePromptsDistinguishTextualCommandsFromExecution(t *testing.T) {
	paths := []string{
		cliAssetPath("lanes", "refine", "draft.md"),
		cliAssetPath("lanes", "refine", "draft-markdown.md"),
		cliAssetPath("lanes", "refine", "review.md"),
		cliAssetPath("lanes", "refine", "patch.md"),
	}
	for _, path := range paths {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}

		prompt := string(body)
		if !strings.Contains(prompt, "no executed shell commands") {
			t.Fatalf("%s must prohibit execution, not textual acceptance command examples:\n%s", path, prompt)
		}

		if !strings.Contains(prompt, "Textual acceptance commands may appear inside the artifact.") {
			t.Fatalf("%s must allow textual acceptance command examples inside artifacts:\n%s", path, prompt)
		}

		if strings.Contains(prompt, "Contract: final-answer artifact; no tool calls, no file edits, no shell commands.") {
			t.Fatalf("%s retains ambiguous shell-command contract:\n%s", path, prompt)
		}
	}
}

// TestRunLaneMainProjectOverlayWrappedByCanonical verifies the
// concatenation order specified in lane-architecture.md §"Prompt
// composition layers": idiom canonical preamble → project preamble
// overlay → brief → project postamble overlay → idiom canonical
// postamble. The project layer extends, never replaces, idiom's
// canonical text.
func TestRunLaneMainProjectOverlayWrappedByCanonical(t *testing.T) {
	briefBody := "# atom\n"
	briefPath := writeBrief(t, briefBody)
	overlayDir := filepath.Join(filepath.Dir(briefPath), ".idiom", "prompts", "lanes", "plan-execute")

	if err := os.MkdirAll(overlayDir, 0o755); err != nil {
		t.Fatalf("mkdir overlay: %v", err)
	}

	if err := os.WriteFile(filepath.Join(overlayDir, "preamble.md"), []byte("PROJECT_PREAMBLE_OVERLAY"), 0o644); err != nil {
		t.Fatalf("write preamble overlay: %v", err)
	}

	if err := os.WriteFile(filepath.Join(overlayDir, "postamble.md"), []byte("PROJECT_POSTAMBLE_OVERLAY"), 0o644); err != nil {
		t.Fatalf("write postamble overlay: %v", err)
	}

	var stdout, stderr bytes.Buffer

	fake := &laneFakeRunner{}
	args := []string{"plan-execute", "--project-root", filepath.Dir(briefPath), briefPath}

	if got := runLaneMainTest(t, args, &stdout, &stderr, fake); got != 0 {
		t.Fatalf("expected exit 0, got %d (stderr=%s)", got, stderr.String())
	}

	prompt := fake.calledPrompt

	canonicalPreIdx := strings.Index(prompt, "Plan-Then-Execute Lane Brief")
	projectPreIdx := strings.Index(prompt, "PROJECT_PREAMBLE_OVERLAY")
	briefIdx := strings.Index(prompt, briefBody)
	projectPostIdx := strings.Index(prompt, "PROJECT_POSTAMBLE_OVERLAY")
	canonicalPostIdx := strings.Index(prompt, "Plan-Then-Execute Closeout")

	idxs := []int{canonicalPreIdx, projectPreIdx, briefIdx, projectPostIdx, canonicalPostIdx}
	for i, idx := range idxs {
		if idx < 0 {
			t.Fatalf("missing layer %d in composed prompt; idxs=%v\n--- prompt head ---\n%s", i, idxs, prompt[:min(800, len(prompt))])
		}
	}

	for i := 1; i < len(idxs); i++ {
		if idxs[i] < idxs[i-1] {
			t.Fatalf("layer order violated at %d→%d; idxs=%v", i-1, i, idxs)
		}
	}
}

// TestRunLaneMainHonorsIdiomConfigEnv verifies that the IDIOM_CONFIG
// env var (used by Colony's run_idiom_impl.sh wrapper) overrides the
// `.idiom/config.json` default when no --config flag is passed. An
// explicit --config still wins over the env var so callers can
// override the env in the rare cases that need it.
func TestRunLaneMainHonorsIdiomConfigEnv(t *testing.T) {
	briefPath := writeBrief(t, "# atom\n")
	t.Setenv("IDIOM_CONFIG", "/custom/idiom-config.json")

	var stdout, stderr bytes.Buffer

	fake := &laneFakeRunner{}

	if got := runLaneMainTest(t, []string{"plan-execute", briefPath}, &stdout, &stderr, fake); got != 0 {
		t.Fatalf("expected exit 0, got %d (stderr=%s)", got, stderr.String())
	}

	if !argsContain(fake.calledArgs, "--config", "/custom/idiom-config.json") {
		t.Fatalf("IDIOM_CONFIG env must override the default --config; got: %v", fake.calledArgs)
	}
}

// TestRunLaneMainConfigFlagBeatsEnv verifies the precedence: an
// explicit --config wins over IDIOM_CONFIG. This matches the
// principle that explicit flags always beat ambient env state.
func TestRunLaneMainConfigFlagBeatsEnv(t *testing.T) {
	briefPath := writeBrief(t, "# atom\n")
	t.Setenv("IDIOM_CONFIG", "/from/env.json")

	var stdout, stderr bytes.Buffer

	fake := &laneFakeRunner{}
	args := []string{"plan-execute", "--config", "/from/flag.json", briefPath}

	if got := runLaneMainTest(t, args, &stdout, &stderr, fake); got != 0 {
		t.Fatalf("expected exit 0, got %d (stderr=%s)", got, stderr.String())
	}

	if !argsContain(fake.calledArgs, "--config", "/from/flag.json") {
		t.Fatalf("--config must beat IDIOM_CONFIG env; got: %v", fake.calledArgs)
	}
}

// TestRunLaneMainAutoRoutesPlanExecuteToBackendsExecutor verifies
// the auto-routing rule for plan-execute: when backends.json declares
// an executor-role alias, the lane forwards that alias as --model.
// This replaces the earlier hardcoded qwen3-coder-30b assertion;
// projects now declare their own executor in backends.json.
func TestRunLaneMainAutoRoutesPlanExecuteToBackendsExecutor(t *testing.T) {
	briefPath := writeBrief(t, "# atom")
	root := filepath.Dir(briefPath)

	if err := os.MkdirAll(filepath.Join(root, ".idiom"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	body := `{"profiles": {"local": {"models": [
		{"alias": "tagged-executor", "roles": ["executor"]}
	]}}}`
	if err := os.WriteFile(filepath.Join(root, ".idiom", "backends.json"), []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	var stdout, stderr bytes.Buffer

	fake := &laneFakeRunner{}
	args := []string{"plan-execute", "--project-root", root, briefPath}

	if got := runLaneMainTest(t, args, &stdout, &stderr, fake); got != 0 {
		t.Fatalf("expected exit 0, got %d (stderr=%s)", got, stderr.String())
	}

	if !argsContain(fake.calledArgs, "--model", "tagged-executor") {
		t.Fatalf("plan-execute must auto-route to backends.json executor role; forwarded: %v", fake.calledArgs)
	}
}

// TestRunLaneMainHonorsExplicitModelOverride verifies that --model
// wins over the auto-routing for plan-execute mode.
func TestRunLaneMainHonorsExplicitModelOverride(t *testing.T) {
	briefPath := writeBrief(t, "# atom")

	var stdout, stderr bytes.Buffer

	fake := &laneFakeRunner{}
	args := []string{"plan-execute", "--model", "qwen3.6-35b", briefPath}

	if got := runLaneMainTest(t, args, &stdout, &stderr, fake); got != 0 {
		t.Fatalf("expected exit 0, got %d", got)
	}

	if !argsContain(fake.calledArgs, "--model", "qwen3.6-35b") {
		t.Fatalf("--model override must reach the underlying subcommand; got: %v", fake.calledArgs)
	}
}

func TestRunLaneMainForwardsNoThinkingOverride(t *testing.T) {
	briefPath := writeBrief(t, "# atom")

	var stdout, stderr bytes.Buffer

	fake := &laneFakeRunner{}
	args := []string{"implement", "--no-thinking", briefPath}

	if got := runLaneMainTest(t, args, &stdout, &stderr, fake); got != 0 {
		t.Fatalf("expected exit 0, got %d stderr=%s", got, stderr.String())
	}

	if !argsContainFlag(fake.calledArgs, "--no-thinking") {
		t.Fatalf("--no-thinking must reach the underlying subcommand; got: %v", fake.calledArgs)
	}
}

// TestRunLaneMainImplementDoesNotAutoRoute verifies that implement
// mode does NOT auto-route to the executor role; only plan-execute
// does. Implement always honors the configured planner unless the
// caller passes --model or --coder explicitly.
func TestRunLaneMainImplementDoesNotAutoRoute(t *testing.T) {
	briefPath := writeBrief(t, "# atom")
	root := filepath.Dir(briefPath)

	if err := os.MkdirAll(filepath.Join(root, ".idiom"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	body := `{"profiles": {"local": {"models": [
		{"alias": "tagged-executor", "roles": ["executor"]}
	]}}}`
	if err := os.WriteFile(filepath.Join(root, ".idiom", "backends.json"), []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	var stdout, stderr bytes.Buffer

	fake := &laneFakeRunner{}
	args := []string{"implement", "--project-root", root, briefPath}

	if got := runLaneMainTest(t, args, &stdout, &stderr, fake); got != 0 {
		t.Fatalf("expected exit 0, got %d", got)
	}

	if argsContain(fake.calledArgs, "--model", "tagged-executor") {
		t.Fatalf("implement mode must NOT auto-route to the executor role; got: %v", fake.calledArgs)
	}
}

// TestRunLaneMainCoderFlagOmitsModelWithoutBackends verifies the
// single-model contract for the legacy --coder flag: with no
// .idiom/backends.json the flag has nothing to resolve and the lane
// must NOT inject a hardcoded fallback alias. The subcommand falls
// back to cfg.LlamaServer for whichever model is configured.
func TestRunLaneMainCoderFlagOmitsModelWithoutBackends(t *testing.T) {
	briefPath := writeBrief(t, "# atom")

	var stdout, stderr bytes.Buffer

	fake := &laneFakeRunner{}
	args := []string{"implement", "--coder", briefPath}

	if got := runLaneMainTest(t, args, &stdout, &stderr, fake); got != 0 {
		t.Fatalf("expected exit 0, got %d", got)
	}

	for i, a := range fake.calledArgs {
		if a == "--model" {
			t.Fatalf("--coder without backends.json must NOT inject --model; got %v at index %d", fake.calledArgs, i)
		}
	}
}

// TestRunLaneMainCoderFlagResolvesViaBackends verifies the new path
// for --coder: when a backends.json declares an executor role, the
// flag resolves to that alias. This is the modern equivalent of the
// old "--coder always means qwen3-coder-30b" behavior, but driven by
// project config rather than a hardcoded global.
func TestRunLaneMainCoderFlagResolvesViaBackends(t *testing.T) {
	briefPath := writeBrief(t, "# atom")
	root := filepath.Dir(briefPath)

	if err := os.MkdirAll(filepath.Join(root, ".idiom"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	body := `{"profiles": {"local": {"models": [
		{"alias": "project-executor", "roles": ["executor"]}
	]}}}`
	if err := os.WriteFile(filepath.Join(root, ".idiom", "backends.json"), []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	var stdout, stderr bytes.Buffer

	fake := &laneFakeRunner{}
	args := []string{"implement", "--project-root", root, "--coder", briefPath}

	if got := runLaneMainTest(t, args, &stdout, &stderr, fake); got != 0 {
		t.Fatalf("expected exit 0, got %d (stderr=%s)", got, stderr.String())
	}

	if !argsContain(fake.calledArgs, "--model", "project-executor") {
		t.Fatalf("--coder must resolve via backends.json executor role; forwarded: %v", fake.calledArgs)
	}
}

// TestRunLaneMainEmitsLaneStartEvent verifies that --json triggers a
// structured idiom.lane.start event so downstream tooling can
// correlate the lane invocation across the planner+executor logs.
func TestRunLaneMainEmitsLaneStartEvent(t *testing.T) {
	briefPath := writeBrief(t, "# atom")

	var stdout, stderr bytes.Buffer

	fake := &laneFakeRunner{}
	args := []string{"plan-execute", "--json", briefPath}

	if got := runLaneMainTest(t, args, &stdout, &stderr, fake); got != 0 {
		t.Fatalf("expected exit 0, got %d", got)
	}

	out := stdout.String()
	if !strings.Contains(out, `"type":"idiom.lane.start"`) {
		t.Fatalf("expected idiom.lane.start event in JSON stream; got: %s", out)
	}

	if !strings.Contains(out, `"mode":"plan-execute"`) {
		t.Fatalf("lane.start event must carry the mode; got: %s", out)
	}
}

// TestRunLaneMainPropagatesExitCode verifies the wrapper returns the
// underlying subcommand's exit code so CI tooling sees the real
// outcome.
func TestRunLaneMainPropagatesExitCode(t *testing.T) {
	briefPath := writeBrief(t, "# atom")

	var stdout, stderr bytes.Buffer

	fake := &laneFakeRunner{exitCode: 7}
	got := runLaneMainTest(t, []string{"plan-execute", briefPath}, &stdout, &stderr, fake)

	if got != 7 {
		t.Fatalf("expected propagated exit code 7, got %d", got)
	}
}

// TestRunLaneMainResolvesProjectRootFromConfig verifies the bug fix:
// project root for overlay lookup must come from cfg.Workspace.Root,
// not the brief's parent directory. Real-world briefs live under
// e.g. <repo>/.planning/<atom>.md, so the brief's parent (.planning)
// is NEVER the right project root in production.
func TestRunLaneMainResolvesProjectRootFromConfig(t *testing.T) {
	// Project layout: brief lives in <root>/.planning/atom.md;
	// overlay file lives in <root>/.idiom/prompts/lanes/.../preamble.md.
	// Brief's parent (the .planning dir) is NOT the project root.
	root := t.TempDir()
	planningDir := filepath.Join(root, ".planning")

	if err := os.MkdirAll(planningDir, 0o755); err != nil {
		t.Fatalf("mkdir planning: %v", err)
	}

	briefPath := filepath.Join(planningDir, "atom.md")
	if err := os.WriteFile(briefPath, []byte(minimalValidPlanExecuteBrief()), 0o644); err != nil {
		t.Fatalf("write brief: %v", err)
	}

	overlayDir := filepath.Join(root, ".idiom", "prompts", "lanes", "plan-execute")
	if err := os.MkdirAll(overlayDir, 0o755); err != nil {
		t.Fatalf("mkdir overlay: %v", err)
	}

	if err := os.WriteFile(filepath.Join(overlayDir, "preamble.md"), []byte("REPO_OVERLAY_SENTINEL"), 0o644); err != nil {
		t.Fatalf("write overlay: %v", err)
	}

	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.EnableWrites = true

	var stdout, stderr bytes.Buffer

	fake := &laneFakeRunner{}
	deps := laneDeps{
		loadConfig:       func(string) (config.Config, error) { return cfg, nil },
		runPlanExecute:   fake.plan,
		runImplement:     fake.implement,
		defaultPromptRun: func(config.Config, string, runOptions, io.Writer) (string, error) { return "", nil },
	}

	if got := runLaneMainWithDeps([]string{"plan-execute", briefPath}, &stdout, &stderr, deps); got != 0 {
		t.Fatalf("expected exit 0, got %d (stderr=%s)", got, stderr.String())
	}

	if !strings.Contains(fake.calledPrompt, "REPO_OVERLAY_SENTINEL") {
		t.Fatalf("project overlay must resolve from cfg.Workspace.Root, not the brief's parent directory; got prompt: %s", fake.calledPrompt[:min(400, len(fake.calledPrompt))])
	}
}

// TestRunLaneMainProjectRootFlagWinsOverConfig verifies the
// precedence: --project-root flag > config workspace.root > brief
// parent dir. The flag is the explicit override; config is the
// production default.
func TestRunLaneMainProjectRootFlagWinsOverConfig(t *testing.T) {
	configRoot := t.TempDir()
	flagRoot := t.TempDir()

	// Overlay only in the flag-root, not the config-root. If the
	// flag wins, the overlay is found; if config wins, no overlay.
	overlayDir := filepath.Join(flagRoot, ".idiom", "prompts", "lanes", "plan-execute")
	if err := os.MkdirAll(overlayDir, 0o755); err != nil {
		t.Fatalf("mkdir overlay: %v", err)
	}

	if err := os.WriteFile(filepath.Join(overlayDir, "preamble.md"), []byte("FLAG_ROOT_OVERLAY"), 0o644); err != nil {
		t.Fatalf("write overlay: %v", err)
	}

	briefPath := writeBrief(t, minimalValidPlanExecuteBrief())
	cfg := config.Default()

	cfg.Workspace.Root = configRoot

	var stdout, stderr bytes.Buffer

	fake := &laneFakeRunner{}
	deps := laneDeps{
		loadConfig:       func(string) (config.Config, error) { return cfg, nil },
		runPlanExecute:   fake.plan,
		runImplement:     fake.implement,
		defaultPromptRun: func(config.Config, string, runOptions, io.Writer) (string, error) { return "", nil },
	}
	args := []string{"plan-execute", "--project-root", flagRoot, briefPath}

	if got := runLaneMainWithDeps(args, &stdout, &stderr, deps); got != 0 {
		t.Fatalf("expected exit 0, got %d", got)
	}

	if !strings.Contains(fake.calledPrompt, "FLAG_ROOT_OVERLAY") {
		t.Fatalf("--project-root flag must override config workspace.root for overlay lookup; prompt: %s", fake.calledPrompt[:min(400, len(fake.calledPrompt))])
	}
}

// TestRunLaneMainCoderFlagAndModelFlagModelWins verifies the
// precedence inside parseLaneOptions: when both --coder and --model
// are passed, --model takes effect. Mirrors Colony's run_idiom_impl.sh
// behavior.
func TestRunLaneMainCoderFlagAndModelFlagModelWins(t *testing.T) {
	briefPath := writeBrief(t, "# atom")

	var stdout, stderr bytes.Buffer

	fake := &laneFakeRunner{}
	args := []string{"implement", "--coder", "--model", "qwen3.6-35b", briefPath}

	if got := runLaneMainTest(t, args, &stdout, &stderr, fake); got != 0 {
		t.Fatalf("expected exit 0, got %d", got)
	}

	if !argsContain(fake.calledArgs, "--model", "qwen3.6-35b") {
		t.Fatalf("--model must beat --coder when both are set; got: %v", fake.calledArgs)
	}

	if argsContain(fake.calledArgs, "--model", "qwen3-coder-30b") {
		t.Fatalf("--model override must NOT leave --coder's qwen3-coder-30b in args: %v", fake.calledArgs)
	}
}

// TestRunLaneMainResumeForwards verifies --resume reaches the
// underlying subcommand so session-resume semantics keep working.
func TestRunLaneMainResumeForwards(t *testing.T) {
	briefPath := writeBrief(t, "# atom")

	var stdout, stderr bytes.Buffer

	fake := &laneFakeRunner{}
	args := []string{"plan-execute", "--resume", "20260509-180015.118", briefPath}

	if got := runLaneMainTest(t, args, &stdout, &stderr, fake); got != 0 {
		t.Fatalf("expected exit 0, got %d", got)
	}

	if !argsContain(fake.calledArgs, "--resume", "20260509-180015.118") {
		t.Fatalf("--resume must forward to the underlying subcommand; got: %v", fake.calledArgs)
	}
}

func TestRunLaneMainDefaultDoesNotForwardIntentLoopFlags(t *testing.T) {
	briefPath := writeBrief(t, "# atom")

	var stdout, stderr bytes.Buffer

	fake := &laneFakeRunner{}

	if got := runLaneMainTest(t, []string{"plan-execute", briefPath}, &stdout, &stderr, fake); got != 0 {
		t.Fatalf("expected exit 0, got %d", got)
	}

	if argsContainFlag(fake.calledArgs, "--intent-loop") || argsContainFlag(fake.calledArgs, "--max-cycles") || argsContainFlag(fake.calledArgs, "--total-budget-tokens") {
		t.Fatalf("default plan-execute must not forward intent-loop flags; got: %v", fake.calledArgs)
	}
}

func TestRunLaneMainIntentLoopFlagsForward(t *testing.T) {
	briefPath := writeBrief(t, "# atom")

	var stdout, stderr bytes.Buffer

	fake := &laneFakeRunner{}
	args := []string{
		"plan-execute",
		"--intent-loop",
		"--max-cycles", "4",
		"--total-budget-tokens", "24000",
		briefPath,
	}

	if got := runLaneMainTest(t, args, &stdout, &stderr, fake); got != 0 {
		t.Fatalf("expected exit 0, got %d stderr=%s", got, stderr.String())
	}

	if !argsContainFlag(fake.calledArgs, "--intent-loop") ||
		!argsContain(fake.calledArgs, "--max-cycles", "4") ||
		!argsContain(fake.calledArgs, "--total-budget-tokens", "24000") {
		t.Fatalf("intent-loop flags must forward to plan-execute; got: %v", fake.calledArgs)
	}
}

func TestRunLaneMainIntentLoopInvalidBudgetExits2(t *testing.T) {
	briefPath := writeBrief(t, "# atom")

	var stdout, stderr bytes.Buffer

	fake := &laneFakeRunner{}
	args := []string{"plan-execute", "--intent-loop", "--total-budget-tokens", "-1", briefPath}

	if got := runLaneMainTest(t, args, &stdout, &stderr, fake); got != 2 {
		t.Fatalf("expected exit 2 for invalid intent-loop budget, got %d", got)
	}
	if fake.calledMode != "" {
		t.Fatalf("invalid budget must not dispatch subcommand, got mode %q", fake.calledMode)
	}
	if !strings.Contains(stderr.String(), "--total-budget-tokens") {
		t.Fatalf("stderr should name invalid budget flag, got %s", stderr.String())
	}
}

// TestRunLaneMainBogusFlagExits2 verifies that flag-parser errors
// surface as exit 2 (not exit 1), keeping CI tooling able to
// distinguish "user passed bad arg" from "lane execution failed".
func TestRunLaneMainBogusFlagExits2(t *testing.T) {
	briefPath := writeBrief(t, "# atom")

	var stdout, stderr bytes.Buffer

	fake := &laneFakeRunner{}
	args := []string{"plan-execute", "--no-such-flag", briefPath}

	if got := runLaneMainTest(t, args, &stdout, &stderr, fake); got != 2 {
		t.Fatalf("expected exit 2 for bad flag, got %d (stderr=%s)", got, stderr.String())
	}
}

// TestRunLaneMainBriefDirRejected verifies that pointing --brief
// at a directory (instead of a file) is caught at parse time with
// exit 2, not deferred to the underlying subcommand.
func TestRunLaneMainBriefDirRejected(t *testing.T) {
	dir := t.TempDir()

	var stdout, stderr bytes.Buffer

	fake := &laneFakeRunner{}

	if got := runLaneMainTest(t, []string{"plan-execute", dir}, &stdout, &stderr, fake); got != 2 {
		t.Fatalf("expected exit 2 when brief path is a directory, got %d", got)
	}

	if !strings.Contains(stderr.String(), "directory") {
		t.Fatalf("error must mention directory; got: %s", stderr.String())
	}
}

// TestRunLaneMainMissingBriefRejected verifies that a brief path
// pointing at a nonexistent file is caught at parse time.
func TestRunLaneMainMissingBriefRejected(t *testing.T) {
	var stdout, stderr bytes.Buffer

	fake := &laneFakeRunner{}

	if got := runLaneMainTest(t, []string{"plan-execute", "/nonexistent/atom.md"}, &stdout, &stderr, fake); got != 2 {
		t.Fatalf("expected exit 2 for missing brief, got %d", got)
	}

	if !strings.Contains(stderr.String(), "not readable") && !strings.Contains(stderr.String(), "no such") {
		t.Fatalf("error must indicate brief unreadable; got: %s", stderr.String())
	}
}

// TestRunLaneMainEmbedsCanonicalRules verifies the Phase 3 rules-embed
// backfill: idiom-canonical generic rules (loop-protocol, brief-skeleton,
// known-traps-generic) are embedded into the binary at build time and
// prepended to every composed lane prompt. Without this layer, projects
// would have to reinvent these working-style rules in every overlay.
func TestRunLaneMainEmbedsCanonicalRules(t *testing.T) {
	briefPath := writeBrief(t, "# atom\n")

	var stdout, stderr bytes.Buffer

	fake := &laneFakeRunner{}

	if got := runLaneMainTest(t, []string{"plan-execute", briefPath}, &stdout, &stderr, fake); got != 0 {
		t.Fatalf("expected exit 0, got %d (stderr=%s)", got, stderr.String())
	}

	prompt := fake.calledPrompt
	required := []string{
		"# Loop Protocol",
		"# Brief Skeleton",
		"# Known Traps — Generic",
	}

	for _, marker := range required {
		if !strings.Contains(prompt, marker) {
			t.Fatalf("composed prompt missing canonical rule header %q; head=%s", marker, prompt[:min(800, len(prompt))])
		}
	}
}

// TestRunLaneMainCanonicalRulesPrecedePreamble verifies the layer order
// from composeLanePromptToFile: rules block is prepended ahead of the
// canonical lane preamble so the model reads working-style rules before
// lane-specific framing.
func TestRunLaneMainCanonicalRulesPrecedePreamble(t *testing.T) {
	briefPath := writeBrief(t, "# atom\n")

	var stdout, stderr bytes.Buffer

	fake := &laneFakeRunner{}

	if got := runLaneMainTest(t, []string{"plan-execute", briefPath}, &stdout, &stderr, fake); got != 0 {
		t.Fatalf("expected exit 0, got %d", got)
	}

	prompt := fake.calledPrompt
	rulesIdx := strings.Index(prompt, "# Loop Protocol")
	preambleIdx := strings.Index(prompt, "Plan-Then-Execute Lane Brief")

	if rulesIdx < 0 || preambleIdx < 0 {
		t.Fatalf("missing layer marker; rules=%d preamble=%d", rulesIdx, preambleIdx)
	}

	if rulesIdx >= preambleIdx {
		t.Fatalf("rules block must precede canonical preamble; rules@%d preamble@%d", rulesIdx, preambleIdx)
	}
}

// TestRunLaneMainProjectRuleReplacesCanonical verifies the same-name
// override semantics for project rules: a file at
// .idiom/prompts/rules/loop-protocol.md replaces the canonical entry
// of the same name in place. The canonical body must NOT appear; the
// project body must.
func TestRunLaneMainProjectRuleReplacesCanonical(t *testing.T) {
	briefPath := writeBrief(t, "# atom\n")
	root := filepath.Dir(briefPath)
	rulesDir := filepath.Join(root, ".idiom", "prompts", "rules")

	if err := os.MkdirAll(rulesDir, 0o755); err != nil {
		t.Fatalf("mkdir rules: %v", err)
	}

	override := "PROJECT_LOOP_PROTOCOL_REPLACEMENT — project supplies its own loop rules"
	if err := os.WriteFile(filepath.Join(rulesDir, "loop-protocol.md"), []byte(override), 0o644); err != nil {
		t.Fatalf("write override: %v", err)
	}

	var stdout, stderr bytes.Buffer

	fake := &laneFakeRunner{}
	args := []string{"plan-execute", "--project-root", root, briefPath}

	if got := runLaneMainTest(t, args, &stdout, &stderr, fake); got != 0 {
		t.Fatalf("expected exit 0, got %d (stderr=%s)", got, stderr.String())
	}

	prompt := fake.calledPrompt
	if !strings.Contains(prompt, "PROJECT_LOOP_PROTOCOL_REPLACEMENT") {
		t.Fatalf("project rule replacement must appear in composed prompt; head=%s", prompt[:min(800, len(prompt))])
	}

	// Canonical loop-protocol.md begins with "# Loop Protocol" — that
	// header must be gone since the project replaced it. The other
	// canonical rules' headers stay (those weren't replaced).
	if strings.Contains(prompt, "# Loop Protocol") {
		t.Fatalf("project replacement must displace canonical loop-protocol.md; canonical header still present")
	}

	if !strings.Contains(prompt, "# Brief Skeleton") || !strings.Contains(prompt, "# Known Traps") {
		t.Fatalf("only the same-named canonical rule should be replaced; other canonical rules went missing")
	}
}

// TestRunLaneMainProjectRuleExtrasAppend verifies the extras semantics:
// project rule files whose names are NOT in the canonical set are
// appended after the canonical block in alphabetical order. This lets
// projects layer in project-specific rules (cpp-safety, gdscript-patterns,
// etc.) without editing the canonical files.
func TestRunLaneMainProjectRuleExtrasAppend(t *testing.T) {
	briefPath := writeBrief(t, "# atom\n")
	root := filepath.Dir(briefPath)
	rulesDir := filepath.Join(root, ".idiom", "prompts", "rules")

	if err := os.MkdirAll(rulesDir, 0o755); err != nil {
		t.Fatalf("mkdir rules: %v", err)
	}

	// Two extras with names that sort z-then-a — verifies alphabetical
	// ordering rather than directory-listing order.
	if err := os.WriteFile(filepath.Join(rulesDir, "z-late-rule.md"), []byte("EXTRA_Z_SENTINEL"), 0o644); err != nil {
		t.Fatalf("write extra z: %v", err)
	}

	if err := os.WriteFile(filepath.Join(rulesDir, "a-early-rule.md"), []byte("EXTRA_A_SENTINEL"), 0o644); err != nil {
		t.Fatalf("write extra a: %v", err)
	}

	var stdout, stderr bytes.Buffer

	fake := &laneFakeRunner{}
	args := []string{"plan-execute", "--project-root", root, briefPath}

	if got := runLaneMainTest(t, args, &stdout, &stderr, fake); got != 0 {
		t.Fatalf("expected exit 0, got %d (stderr=%s)", got, stderr.String())
	}

	prompt := fake.calledPrompt

	canonicalLastIdx := strings.Index(prompt, "# Known Traps — Generic")
	extraAIdx := strings.Index(prompt, "EXTRA_A_SENTINEL")
	extraZIdx := strings.Index(prompt, "EXTRA_Z_SENTINEL")

	if canonicalLastIdx < 0 || extraAIdx < 0 || extraZIdx < 0 {
		t.Fatalf("missing layer marker; canonical=%d a=%d z=%d", canonicalLastIdx, extraAIdx, extraZIdx)
	}

	if !(canonicalLastIdx < extraAIdx && extraAIdx < extraZIdx) {
		t.Fatalf("extras must follow canonical block in alphabetical order; canonical@%d a@%d z@%d", canonicalLastIdx, extraAIdx, extraZIdx)
	}
}

// TestRunLaneMainEmptyProjectRulesDirGraceful verifies that a project
// with an empty .idiom/prompts/rules/ directory degrades silently —
// canonical rules still load, no panic, no error.
func TestRunLaneMainEmptyProjectRulesDirGraceful(t *testing.T) {
	briefPath := writeBrief(t, "# atom\n")
	root := filepath.Dir(briefPath)

	if err := os.MkdirAll(filepath.Join(root, ".idiom", "prompts", "rules"), 0o755); err != nil {
		t.Fatalf("mkdir empty rules dir: %v", err)
	}

	var stdout, stderr bytes.Buffer

	fake := &laneFakeRunner{}
	args := []string{"plan-execute", "--project-root", root, briefPath}

	if got := runLaneMainTest(t, args, &stdout, &stderr, fake); got != 0 {
		t.Fatalf("expected exit 0, got %d (stderr=%s)", got, stderr.String())
	}

	if !strings.Contains(fake.calledPrompt, "# Loop Protocol") {
		t.Fatalf("canonical rules must still load when the project rules dir is empty")
	}
}

// argsContain returns true if the args slice contains the flag
// followed immediately by the value.
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

func flagValues(args []string, flag string) []string {
	var values []string

	for i, a := range args {
		if a == flag && i+1 < len(args) {
			values = append(values, args[i+1])
		}
	}

	return values
}

// runLaneMainTest is the test-friendly entry to the lane wrapper. It
// resolves the in-test config (a minimal default with no actual
// LLM endpoint), installs a no-op brief-lint stub so the default-on
// preflight doesn't try to shell out to python3 in the test
// environment, and dispatches to the fake runner the test owns.
func runLaneMainTest(t *testing.T, args []string, stdout, stderr io.Writer, fake *laneFakeRunner) int {
	t.Helper()
	stubLaneBriefLint(t, func([]string, io.Writer, io.Writer) int { return 0 })

	cfg := config.Default()

	cfg.Workspace.EnableWrites = true

	deps := laneDeps{
		loadConfig:       func(string) (config.Config, error) { return cfg, nil },
		runPlanExecute:   fake.plan,
		runImplement:     fake.implement,
		defaultPromptRun: func(config.Config, string, runOptions, io.Writer) (string, error) { return "", nil },
	}

	return runLaneMainWithDeps(args, stdout, stderr, deps)
}

// stubLaneBriefLint swaps in a test-controlled lint runner and
// restores the production one on cleanup. Tests that want to
// exercise the lint preflight reassign before calling
// runLaneMainTest; the helper installs a no-op stub by default.
func stubLaneBriefLint(t *testing.T, stub func(args []string, stdout, stderr io.Writer) int) {
	t.Helper()

	orig := laneBriefLintRunner

	laneBriefLintRunner = stub

	t.Cleanup(func() { laneBriefLintRunner = orig })
}

func stubLanePostflightRunner(t *testing.T, stub func(args []string, stdout, stderr io.Writer) int) {
	t.Helper()

	orig := lanePostflightRunner

	lanePostflightRunner = stub

	t.Cleanup(func() { lanePostflightRunner = orig })
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
