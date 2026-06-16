package probe

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRunProbeLanePassesForBothModesByDefault is the canonical
// happy path: with --mode all (the default), the probe runs both
// implement and plan-execute under both BuildMode and PlanMode (4
// cases total) and exits 0 when all pass. The PASS lines hit
// stdout so shell wrappers can grep the result.
func TestRunProbeLanePassesForBothModesByDefault(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{"lane"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("default probe must exit 0; got %d (stderr=%s)", code, stderr.String())
	}

	out := stdout.String()
	for _, want := range []string{"PASS implement(build)", "PASS plan-execute(build)"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in output: %s", want, out)
		}
	}
}

// TestRunProbeLaneAcceptsExplicitModeFlag verifies the filter:
// passing --mode implement runs only the implement family (still
// covering both agent modes within it). plan-execute output must
// be absent.
func TestRunProbeLaneAcceptsExplicitModeFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{"lane", "--mode", "implement"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("implement probe must exit 0; got %d", code)
	}

	out := stdout.String()
	if !strings.Contains(out, "PASS implement(build)") {
		t.Fatalf("expected PASS implement(build): %s", out)
	}

	if strings.Contains(out, "plan-execute") {
		t.Fatalf("unexpected plan-execute output: %s", out)
	}
}

// TestRunProbeLaneRejectsUnknownMode verifies the flag-error path:
// an unrecognized --mode value exits 2 (flag-class) rather than
// silently picking a fallback. The error names the bad flag.
func TestRunProbeLaneRejectsUnknownMode(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{"lane", "--mode", "nonsense"}, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("unknown --mode must exit 2; got %d", code)
	}

	if !strings.Contains(stderr.String(), "not recognized") {
		t.Fatalf("error should explain bad flag; got: %s", stderr.String())
	}
}

// TestRunProbeLaneFallsBackWhenConfigMissing exercises the
// config-load fallback: pointing --config at a path that doesn't
// exist must NOT fail the probe; the probe loads the compiled-default
// tool surface and still verifies the routing shape. Important because
// operators run the probe from arbitrary working directories where
// config.json may not be present.
func TestRunProbeLaneFallsBackWhenConfigMissing(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{"lane", "--config", "/definitely/no/config/here.json"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("missing config must not fail the probe; got %d (stderr=%s)", code, stderr.String())
	}

	if !strings.Contains(stderr.String(), "tool surface from --config=") {
		t.Fatalf("stderr should explain the resolved source: %s", stderr.String())
	}

	if !strings.Contains(stdout.String(), "PASS implement(build)") {
		t.Fatalf("probe should still PASS via the default surface: %s", stdout.String())
	}
}

// TestRunProbeLaneAcceptsCoderFlagAsNoOp verifies the spec-parity
// flag added in the post-Phase-6 audit. --coder is accepted (the
// design doc lists it) but the in-process probe doesn't depend on
// the model alias, so the only observable effect is a stderr note
// confirming the flag landed.
func TestRunProbeLaneAcceptsCoderFlagAsNoOp(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{"lane", "--coder"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("--coder must not affect exit code; got %d", code)
	}

	if !strings.Contains(stderr.String(), "--coder accepted") {
		t.Fatalf("stderr should acknowledge --coder explicitly: %s", stderr.String())
	}
}

// TestRunProbeLaneResolvesConfigUnderProjectRoot closes the gap I
// originally dodged: the probe should match `idiom lane`'s config
// resolution so an operator running it from any cwd hits the same
// tool surface they'd see at lane invocation time. Pass
// --project-root pointing at a temp dir that holds a minimal
// .idiom/config.json and assert the probe loaded it (vs falling
// back to the synthetic surface).
func TestRunProbeLaneResolvesConfigUnderProjectRoot(t *testing.T) {
	root := t.TempDir()
	cfgDir := filepath.Join(root, ".idiom")

	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Minimal valid config: workspace root + write enable + a
	// shell are enough for NewManager + AllowedTools to surface
	// non-empty.
	body := `{
  "app_name": "probe-test",
  "llama_server": {"kind": "mlx", "host": "127.0.0.1", "port": 1, "model": "x"},
  "workspace": {"root": "` + root + `", "cwd": "` + root + `", "shell": "/bin/sh", "command_timeout_seconds": 60, "max_output_bytes": 1024, "enable_writes": false}
}`
	if err := os.WriteFile(filepath.Join(cfgDir, "config.json"), []byte(body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var stdout, stderr bytes.Buffer

	code := Run([]string{"lane", "--project-root", root}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("probe with project-root must exit 0; got %d (stderr=%s)", code, stderr.String())
	}

	if !strings.Contains(stderr.String(), "tool surface from project-root") {
		t.Fatalf("stderr should report project-root resolution: %s", stderr.String())
	}

	if strings.Contains(stderr.String(), "falling back to synthetic") {
		t.Fatalf("project-root config must NOT trigger the synthetic fallback: %s", stderr.String())
	}

	if !strings.Contains(stdout.String(), "PASS implement(build)") {
		t.Fatalf("probe must PASS with the resolved config: %s", stdout.String())
	}
}

// TestRunProbeLaneExplicitConfigOverridesProjectRoot verifies the
// precedence in resolveProbeConfigPath: --config wins over the
// project-root scan, so an operator can point the probe at an
// alternate config (e.g. a CI-only variant) without moving files.
func TestRunProbeLaneExplicitConfigOverridesProjectRoot(t *testing.T) {
	root := t.TempDir()
	// Drop a config under .idiom/ that would normally win; the
	// explicit --config should override it.
	if err := os.MkdirAll(filepath.Join(root, ".idiom"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, ".idiom", "config.json"),
		[]byte(`{"workspace": {"root": "/x"}}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	var stdout, stderr bytes.Buffer

	code := Run([]string{"lane", "--project-root", root, "--config", "/no/such/explicit.json"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("explicit --config must not block exit 0; got %d", code)
	}

	if !strings.Contains(stderr.String(), "tool surface from --config=") {
		t.Fatalf("explicit --config to a missing path should still show its source, proving it overrode the in-root file: %s", stderr.String())
	}
}

// TestRunProbeLaneCrossModeCoverage verifies the regression-class
// guard added in this pass: every user-facing probe runs under both
// BuildMode and PlanMode so a future refactor that makes
// normalizeTurnRoutePlan mode-dependent for explicit_task_family
// flips this from PASS to FAIL. Today the routing is mode-stable
// for that family and all four cases pass.
func TestRunProbeLaneCrossModeCoverage(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{"lane"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("cross-mode probe must exit 0; got %d (stderr=%s)", code, stderr.String())
	}

	out := stdout.String()
	for _, want := range []string{
		"PASS implement(build)",
		"PASS implement(plan)",
		"PASS plan-execute(build)",
		"PASS plan-execute(plan)",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in output: %s", want, out)
		}
	}
}

// TestRunProbeLaneHelpExitsZero verifies the ErrHelp handling: a
// user asking for help should get usage on stderr and exit 0, not
// the flag-parse error code (2). Conventional CLI behavior; without
// this fix `idiom probe lane --help && echo ok` would never print
// "ok".
func TestRunProbeLaneHelpExitsZero(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{"lane", "--help"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("--help must exit 0; got %d", code)
	}
}

// TestRunProbeMainUnknownSubcommandExitsTwo verifies the dispatch
// surface: a subcommand we don't support (e.g. typo or a future
// `probe backend`) surfaces a clean usage error instead of silent
// success.
func TestRunProbeMainUnknownSubcommandExitsTwo(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{"definitely-not-a-subcommand"}, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("unknown subcommand must exit 2; got %d", code)
	}

	if !strings.Contains(stderr.String(), "unknown subcommand") {
		t.Fatalf("stderr should name the subcommand: %s", stderr.String())
	}
}
