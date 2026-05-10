package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"idiom/internal/config"
)

// laneFakeRunner captures the arguments handed to the underlying
// idiom subcommand so tests can assert on what the wrapper forwarded.
type laneFakeRunner struct {
	calledMode  string
	calledArgs  []string
	calledPrompt string
	exitCode   int
}

func (f *laneFakeRunner) plan(args []string, _, _ io.Writer, _ promptRunner) int {
	f.calledMode = "plan-execute"
	f.calledArgs = append([]string(nil), args...)
	f.calledPrompt = readPromptFromArgs(args)
	return f.exitCode
}

func (f *laneFakeRunner) implement(args []string, _, _ io.Writer, _ promptRunner) int {
	f.calledMode = "implement"
	f.calledArgs = append([]string(nil), args...)
	f.calledPrompt = readPromptFromArgs(args)
	return f.exitCode
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
	required := []string{
		"Plan-Then-Execute Lane Brief",
		"You are the PLANNER",
		"submit_plan",
	}
	for _, marker := range required {
		if !strings.Contains(prompt, marker) {
			t.Fatalf("plan-execute composed prompt missing canonical preamble marker %q; head=%s", marker, prompt[:min(400, len(prompt))])
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
	if err := os.WriteFile(briefPath, []byte("# atom\nFiles: x"), 0o644); err != nil {
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

	briefPath := writeBrief(t, "# atom")
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

// runLaneMainTest is the test-friendly entry to the lane wrapper. It
// resolves the in-test config (a minimal default with no actual
// LLM endpoint) and dispatches to the fake runner the test owns.
func runLaneMainTest(t *testing.T, args []string, stdout, stderr io.Writer, fake *laneFakeRunner) int {
	t.Helper()
	cfg := config.Default()
	cfg.Workspace.EnableWrites = true
	deps := laneDeps{
		loadConfig:        func(string) (config.Config, error) { return cfg, nil },
		runPlanExecute:    fake.plan,
		runImplement:      fake.implement,
		defaultPromptRun:  func(config.Config, string, runOptions, io.Writer) (string, error) { return "", nil },
	}
	return runLaneMainWithDeps(args, stdout, stderr, deps)
}

