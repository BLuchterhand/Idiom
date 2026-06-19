package sweeval

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"idiom/internal/evidence/eval"
)

func sweEvalTestRepoRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get cwd: %v", err)
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("repo root not found from cwd")
		}

		dir = parent
	}
}

func TestLiveContractRunnerWritesTurnsCompletelyToPTY(t *testing.T) {
	scriptPath := filepath.Join(sweEvalTestRepoRoot(t), "scripts", "live-contract-runner.sh")
	body, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatalf("read live contract runner: %v", err)
	}

	text := string(body)
	for _, want := range []string{
		"def write_all(fd, data):",
		`write_all(master, turn.encode("utf-8") + b"\r")`,
		`write_all(master, b"\x03")`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("live contract runner must use complete PTY writes; missing %q", want)
		}
	}

	if strings.Contains(text, `os.write(master, turn.encode("utf-8") + b"\r")`) ||
		strings.Contains(text, `os.write(master, b"\x03")`) {
		t.Fatal("live contract runner must not use single os.write calls for PTY input")
	}
}

func TestRunSuiteOnlyPassesValidSuite(t *testing.T) {
	dir := t.TempDir()
	suitePath := filepath.Join(dir, "suite.json")
	writeSWESmokeSuite(t, suitePath, "")

	var stdout, stderr bytes.Buffer

	code := run([]string{"-suite", suitePath, "-suite-only"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("expected exit 0, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}

	if !strings.Contains(stdout.String(), "PASS cli-smoke-suite") || !strings.Contains(stdout.String(), "Lint: PASS") {
		t.Fatalf("expected passing suite report, got %s", stdout.String())
	}
}

func TestRunEvaluatesLogsDirectory(t *testing.T) {
	dir := t.TempDir()
	suitePath := filepath.Join(dir, "suite.json")
	logsDir := filepath.Join(dir, "logs")

	if err := os.MkdirAll(logsDir, 0o755); err != nil {
		t.Fatalf("mkdir logs: %v", err)
	}

	writeSWESmokeSuite(t, suitePath, "")
	writeSWESmokeLog(t, filepath.Join(logsDir, "direct.jsonl"))

	var stdout, stderr bytes.Buffer

	code := run([]string{"-suite", suitePath, "-logs", logsDir}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("expected exit 0, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}

	for _, want := range []string{"PASS cli-smoke-suite", "Results: passed=1 failed=0", "Control rails dashboard:", "- PASS direct"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("expected suite output to contain %q, got %s", want, stdout.String())
		}
	}
}

func TestRunLiveGeneratesLogsAndScoresThem(t *testing.T) {
	dir := t.TempDir()
	suitePath := filepath.Join(dir, "suite.json")
	logsDir := filepath.Join(dir, "generated")

	writeSWESmokeSuite(t, suitePath, "")

	command := strings.Join([]string{
		`printf '%s\n'`,
		`'{"type":"message","role":"user","content":"Explain routing."}'`,
		`'{"type":"message","role":"route_plan","content":"initial=main reason=direct_conversation contract=direct_answer"}'`,
		`'{"type":"message","role":"assistant","content":"Routing is direct for this smoke test."}'`,
		`> {log_path}`,
	}, " ")

	var stdout, stderr bytes.Buffer

	code := run([]string{"-suite", suitePath, "-run-live", "-generated-logs", logsDir, "-runner-command", command}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("expected exit 0, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}

	if _, err := os.Stat(filepath.Join(logsDir, "direct.jsonl")); err != nil {
		t.Fatalf("expected generated log: %v", err)
	}
}

func TestRunLivePassesMultiTurnMetadata(t *testing.T) {
	dir := t.TempDir()
	suitePath := filepath.Join(dir, "suite.json")
	logsDir := filepath.Join(dir, "generated")

	writeSWEMultiTurnSmokeSuite(t, suitePath)

	command := strings.Join([]string{
		`test "$IDIOM_SWE_TURN_COUNT" = "2"`,
		`&& test "$IDIOM_SWE_FAMILY" = "direct_question"`,
		`&& test "$IDIOM_SWE_TURNS_JSON" = '["first turn","second turn"]'`,
		`&& printf '%s\n'`,
		`'{"type":"message","role":"user","content":"first turn"}'`,
		`'{"type":"message","role":"assistant","content":"first answer"}'`,
		`'{"type":"message","role":"user","content":"second turn"}'`,
		`'{"type":"message","role":"route_plan","content":"initial=main reason=direct_conversation contract=direct_answer"}'`,
		`'{"type":"message","role":"assistant","content":"second answer"}'`,
		`> {log_path}`,
	}, " ")

	var stdout, stderr bytes.Buffer

	code := run([]string{"-suite", suitePath, "-run-live", "-generated-logs", logsDir, "-runner-command", command}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("expected exit 0, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}

	if !strings.Contains(stdout.String(), "PASS cli-smoke-suite") {
		t.Fatalf("expected passing suite, got stdout=%s stderr=%s", stdout.String(), stderr.String())
	}
}

func TestRunLivePassesRunnerModeMetadata(t *testing.T) {
	dir := t.TempDir()
	suitePath := filepath.Join(dir, "suite.json")
	logsDir := filepath.Join(dir, "generated")
	suite := eval.SWESuite{
		Name: "cli-runner-mode-suite",
		Tasks: []eval.SWETask{{
			ID:         "scripted",
			Family:     "implementation",
			RunnerMode: "implement",
			Prompt:     "Implement routing.",
			Tags:       []string{"implement"},
			Scenario: eval.Scenario{
				Name:                "scripted",
				PromptContains:      []string{"Implement routing"},
				RequireChangedFiles: []string{"README.md"},
				RequireVerification: "none",
			},
			Oracle: eval.SWEOracle{
				RequireChangedFiles: []string{"README.md"},
			},
		}},
	}

	if err := writeSWESuite(suitePath, suite); err != nil {
		t.Fatalf("write suite: %v", err)
	}

	command := strings.Join([]string{
		`test "$IDIOM_SWE_RUNNER_MODE" = "implement"`,
		`&& printf '%s\n'`,
		`'{"type":"message","role":"user","content":"Implement routing."}'`,
		`'{"type":"tool_request","tool":"edit_file","path":"README.md"}'`,
		`'{"type":"tool_result","tool":"edit_file","path":"README.md","summary":"Edited README.md"}'`,
		`'{"type":"message","role":"assistant","content":"Changed files:\n- README.md"}'`,
		`> {log_path}`,
	}, " ")

	var stdout, stderr bytes.Buffer

	code := run([]string{"-suite", suitePath, "-run-live", "-generated-logs", logsDir, "-runner-command", command}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("expected exit 0, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
}

func TestRunLiveContinueOnErrorRunsRemainingTasksAndWritesJSONLFailure(t *testing.T) {
	dir := t.TempDir()
	suitePath := filepath.Join(dir, "suite.json")
	logsDir := filepath.Join(dir, "generated")

	writeSWETwoTaskSmokeSuite(t, suitePath)

	command := strings.Join([]string{
		`if test "$IDIOM_SWE_TASK_ID" = "first"; then`,
		`printf 'not json\n' > {log_path}; exit 7;`,
		`else`,
		`printf '%s\n'`,
		`'{"type":"message","role":"user","content":"Explain routing."}'`,
		`'{"type":"message","role":"route_plan","content":"initial=main reason=direct_conversation contract=direct_answer"}'`,
		`'{"type":"message","role":"assistant","content":"Routing is direct for this smoke test."}'`,
		`> {log_path};`,
		`fi`,
	}, " ")

	var stdout, stderr bytes.Buffer

	code := run([]string{"-suite", suitePath, "-run-live", "-run-live-continue-on-error", "-generated-logs", logsDir, "-runner-command", command}, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("expected failing suite exit 1, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}

	if !strings.Contains(stderr.String(), "failed first") || !strings.Contains(stderr.String(), "[2/2] finished second") {
		t.Fatalf("expected continue-on-error progress, got stderr=%s", stderr.String())
	}

	firstLog, err := os.ReadFile(filepath.Join(logsDir, "first.jsonl"))
	if err != nil {
		t.Fatalf("read first log: %v", err)
	}

	if !strings.Contains(string(firstLog), `"type":"error"`) || !strings.Contains(string(firstLog), "live runner task failed") {
		t.Fatalf("expected synthetic JSONL error log, got %s", firstLog)
	}

	if _, err := eval.LoadRecords(filepath.Join(logsDir, "first.jsonl")); err != nil {
		t.Fatalf("synthetic error log must be loadable JSONL: %v", err)
	}

	if _, err := os.Stat(filepath.Join(logsDir, "second.jsonl")); err != nil {
		t.Fatalf("expected second task log after first failure: %v", err)
	}
}

func TestRunLiveScoresNonzeroRunnerWhenLogIsLoadable(t *testing.T) {
	dir := t.TempDir()
	suitePath := filepath.Join(dir, "suite.json")
	logsDir := filepath.Join(dir, "generated")

	writeSWESmokeSuite(t, suitePath, "")

	command := strings.Join([]string{
		`printf '%s\n'`,
		`'{"type":"message","role":"user","content":"Explain routing."}'`,
		`'{"type":"message","role":"route_plan","content":"initial=main reason=direct_conversation contract=direct_answer"}'`,
		`'{"type":"message","role":"assistant","content":"Routing is direct for this smoke test."}'`,
		`> {log_path};`,
		`exit 1`,
	}, " ")

	var stdout, stderr bytes.Buffer

	code := run([]string{"-suite", suitePath, "-run-live", "-generated-logs", logsDir, "-runner-command", command}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("expected scoreable nonzero runner log to pass, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}

	if !strings.Contains(stderr.String(), "runner exited nonzero") {
		t.Fatalf("expected nonzero runner notice, got stderr=%s", stderr.String())
	}
}

func TestLiveContractRunnerRollsBackWorkspaceWithFakeBinary(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skipf("bash not available: %v", err)
	}

	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	logPath := filepath.Join(dir, "contract.jsonl")
	fakeBin := filepath.Join(dir, "fake-idiom")

	if err := os.WriteFile(configPath, []byte(`{"chat":{"system_prompt_files":[]},"workspace":{},"llama_server":{},"implementer":{},"route_classifier":{}}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	fake := `#!/usr/bin/env bash
set -euo pipefail
config=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    -config) config="$2"; shift 2 ;;
    *) shift ;;
  esac
done
python3 - "$config" <<'PY'
import json
import os
import pathlib
import sys
cfg = json.load(open(sys.argv[1], encoding="utf-8"))
root = pathlib.Path(cfg["workspace"]["root"])
debug = pathlib.Path(cfg["chat"]["debug_log_dir"])
debug.mkdir(parents=True, exist_ok=True)
pathlib.Path("logs/telemetry").mkdir(parents=True, exist_ok=True)
pathlib.Path("logs/telemetry/cwd-leak.jsonl").write_text("{}\n", encoding="utf-8")
(root / "internal/labels/labels.go").write_text('package labels\n\nfunc ShortLabel() string {\n\treturn "Save"\n}\n', encoding="utf-8")
with (debug / "fake-session.jsonl").open("w", encoding="utf-8") as f:
    f.write(json.dumps({"type":"session_start","workspace_root":str(root)}) + "\n")
    f.write(json.dumps({"type":"message","role":"user","content":"Implement this feature"}) + "\n")
    f.write(json.dumps({"type":"tool_request","step":1,"tool":"edit_file"}) + "\n")
    f.write(json.dumps({"type":"tool_result","step":1,"tool":"edit_file","summary":"Edited internal/labels/labels.go","error":""}) + "\n")
    f.write(json.dumps({"type":"phase","lane":"implement","phase":"verify","state":"done"}) + "\n")
    f.write(json.dumps({"type":"message","role":"assistant","content":"Done."}) + "\n")
PY
`
	if err := os.WriteFile(fakeBin, []byte(fake), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}

	scriptPath, err := filepath.Abs(filepath.Join(sweEvalTestRepoRoot(t), "scripts", "live-contract-runner.sh"))
	if err != nil {
		t.Fatalf("script path: %v", err)
	}

	fixturePath, err := filepath.Abs(filepath.Join(sweEvalTestRepoRoot(t), "testdata", "live-contracts", "fixtures", "frozen-label-repo"))
	if err != nil {
		t.Fatalf("fixture path: %v", err)
	}

	fixtureLeak := filepath.Join(fixturePath, "logs", "telemetry", "cwd-leak.jsonl")

	_ = os.Remove(fixtureLeak)

	cmd := exec.Command("bash", scriptPath)

	cmd.Dir = fixturePath
	cmd.Env = append(os.Environ(),
		"IDIOM_BIN="+fakeBin,
		"IDIOM_CONFIG="+configPath,
		"IDIOM_SWE_FIXTURE="+fixturePath,
		"IDIOM_SWE_LOG="+logPath,
		"IDIOM_SWE_PROMPT=Implement this feature",
		"IDIOM_SWE_TASK_TIMEOUT=15",
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("live contract runner failed: %v\n%s", err, output)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}

	for _, want := range []string{
		`"type": "idiom.live_contract.workspace_prepared"`,
		`"type": "idiom.live_contract.rollback"`,
		`"status": "clean"`,
		`"changed_before_reset": ["internal/labels/labels.go"]`,
		`"changed_after_reset": []`,
	} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("expected log to contain %q, got:\n%s", want, data)
		}
	}

	records, err := eval.LoadRecords(logPath)
	if err != nil {
		t.Fatalf("load records: %v", err)
	}

	metrics := eval.Analyze(records)
	foundChanged := false

	for _, path := range metrics.ChangedFiles {
		if strings.EqualFold(strings.TrimSpace(path), "internal/labels/labels.go") {
			foundChanged = true
			break
		}
	}

	if !foundChanged {
		t.Fatalf("expected rollback changed-file evidence, got %#v", metrics.ChangedFiles)
	}

	if _, err := os.Stat(fixtureLeak); !os.IsNotExist(err) {
		t.Fatalf("live runner leaked cwd telemetry into fixture source: %v", err)
	}
}

func TestLiveContractRunnerSkipsStaleWriteWhenLogOwnershipChanges(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skipf("bash not available: %v", err)
	}

	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	logPath := filepath.Join(dir, "contract.jsonl")
	fakeBin := filepath.Join(dir, "fake-idiom")

	if err := os.WriteFile(configPath, []byte(`{"chat":{"system_prompt_files":[]},"workspace":{},"llama_server":{},"implementer":{},"route_classifier":{}}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	fake := `#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' '{"type":"message","role":"user","content":"newer run owns this log"}' > "$IDIOM_SWE_LOG"
printf '%s\n' 'newer-run' > "$IDIOM_SWE_LOG.owner"
config=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    -config) config="$2"; shift 2 ;;
    *) shift ;;
  esac
done
python3 - "$config" <<'PY'
import json
import pathlib
import sys
cfg = json.load(open(sys.argv[1], encoding="utf-8"))
debug = pathlib.Path(cfg["chat"]["debug_log_dir"])
debug.mkdir(parents=True, exist_ok=True)
with (debug / "stale-session.jsonl").open("w", encoding="utf-8") as f:
    f.write('{"type":"message","role":"user","content":"stale run"}\n')
PY
`
	if err := os.WriteFile(fakeBin, []byte(fake), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}

	scriptPath, err := filepath.Abs(filepath.Join(sweEvalTestRepoRoot(t), "scripts", "live-contract-runner.sh"))
	if err != nil {
		t.Fatalf("script path: %v", err)
	}

	fixturePath, err := filepath.Abs(filepath.Join(sweEvalTestRepoRoot(t), "testdata", "live-contracts", "fixtures", "frozen-label-repo"))
	if err != nil {
		t.Fatalf("fixture path: %v", err)
	}

	cmd := exec.Command("bash", scriptPath)

	cmd.Dir = fixturePath
	cmd.Env = append(os.Environ(),
		"IDIOM_BIN="+fakeBin,
		"IDIOM_CONFIG="+configPath,
		"IDIOM_SWE_FIXTURE="+fixturePath,
		"IDIOM_SWE_LOG="+logPath,
		"IDIOM_SWE_PROMPT=Implement this feature",
		"IDIOM_SWE_TASK_TIMEOUT=15",
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("live contract runner failed: %v\n%s", err, output)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}

	if !strings.Contains(string(data), "newer run owns this log") {
		t.Fatalf("expected newer owner log to remain, got:\n%s", data)
	}

	for _, stale := range []string{"stale run", "idiom.live_contract.rollback", "idiom.live_contract.workspace_prepared"} {
		if strings.Contains(string(data), stale) {
			t.Fatalf("stale runner wrote %q into newer log:\n%s", stale, data)
		}
	}
}

func TestLiveContractRunnerArchivesGitRefFixture(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skipf("bash not available: %v", err)
	}

	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}

	dir := t.TempDir()
	sourceRepo := filepath.Join(dir, "source")

	if err := os.MkdirAll(filepath.Join(sourceRepo, "internal", "labels"), 0o755); err != nil {
		t.Fatalf("mkdir source repo: %v", err)
	}

	if err := os.WriteFile(filepath.Join(sourceRepo, "internal", "labels", "labels.go"), []byte("package labels\n\nfunc ShortLabel() string {\n\treturn \"Save changes\"\n}\n"), 0o644); err != nil {
		t.Fatalf("write source file: %v", err)
	}

	runGit := func(args ...string) {
		t.Helper()

		cmd := exec.Command("git", append([]string{"-C", sourceRepo}, args...)...)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, output)
		}
	}
	runGit("init", "-q")
	runGit("config", "user.email", "live-contracts@example.invalid")
	runGit("config", "user.name", "Idiom Live Contracts")
	runGit("add", "-A")
	runGit("commit", "-q", "-m", "frozen source")

	commit := strings.TrimSpace(string(mustCommandOutput(t, "git", "-C", sourceRepo, "rev-parse", "HEAD")))
	if err := os.WriteFile(filepath.Join(sourceRepo, "dirty-untracked.txt"), []byte("must not enter archived fixture\n"), 0o644); err != nil {
		t.Fatalf("write untracked source file: %v", err)
	}

	configPath := filepath.Join(dir, "config.json")
	logPath := filepath.Join(dir, "contract.jsonl")
	fakeBin := filepath.Join(dir, "fake-idiom")

	if err := os.WriteFile(configPath, []byte(`{"chat":{"system_prompt_files":[]},"workspace":{},"llama_server":{},"implementer":{},"route_classifier":{}}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	fake := `#!/usr/bin/env bash
set -euo pipefail
config=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    -config) config="$2"; shift 2 ;;
    *) shift ;;
  esac
done
python3 - "$config" <<'PY'
import json
import pathlib
import sys
cfg = json.load(open(sys.argv[1], encoding="utf-8"))
root = pathlib.Path(cfg["workspace"]["root"])
debug = pathlib.Path(cfg["chat"]["debug_log_dir"])
debug.mkdir(parents=True, exist_ok=True)
if (root / "dirty-untracked.txt").exists():
    raise SystemExit("untracked source file leaked into archived fixture")
(root / "internal/labels/labels.go").write_text('package labels\n\nfunc ShortLabel() string {\n\treturn "Save"\n}\n', encoding="utf-8")
with (debug / "fake-session.jsonl").open("w", encoding="utf-8") as f:
    f.write(json.dumps({"type":"session_start","workspace_root":str(root)}) + "\n")
    f.write(json.dumps({"type":"message","role":"user","content":"Implement this feature"}) + "\n")
    f.write(json.dumps({"type":"phase","lane":"implement","phase":"verify","state":"done"}) + "\n")
    f.write(json.dumps({"type":"message","role":"assistant","content":"Done."}) + "\n")
PY
`
	if err := os.WriteFile(fakeBin, []byte(fake), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}

	scriptPath, err := filepath.Abs(filepath.Join(sweEvalTestRepoRoot(t), "scripts", "live-contract-runner.sh"))
	if err != nil {
		t.Fatalf("script path: %v", err)
	}

	cmd := exec.Command("bash", scriptPath)

	cmd.Dir = sourceRepo
	cmd.Env = append(os.Environ(),
		"IDIOM_BIN="+fakeBin,
		"IDIOM_CONFIG="+configPath,
		"IDIOM_SWE_FIXTURE_GIT_REPO="+sourceRepo,
		"IDIOM_SWE_FIXTURE_GIT_REF="+commit,
		"IDIOM_SWE_LOG="+logPath,
		"IDIOM_SWE_PROMPT=Implement this feature",
		"IDIOM_SWE_TASK_TIMEOUT=15",
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("live contract git-ref runner failed: %v\n%s", err, output)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}

	for _, want := range []string{
		`"fixture_source_kind": "git_ref"`,
		`"fixture_git_ref": "` + commit + `"`,
		`"source_commit": "` + commit + `"`,
		`"changed_before_reset": ["internal/labels/labels.go"]`,
		`"status": "clean"`,
	} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("expected log to contain %q, got:\n%s", want, data)
		}
	}
}

func mustCommandOutput(t *testing.T, name string, args ...string) []byte {
	t.Helper()

	output, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v failed: %v\n%s", name, args, err, output)
	}

	return output
}

func TestRunLiveDerivesTaskTimeoutFromOracleAndReportsProgress(t *testing.T) {
	dir := t.TempDir()
	suitePath := filepath.Join(dir, "suite.json")
	logsDir := filepath.Join(dir, "generated")
	maxDurationMillis := int64(45000)
	requireApproval := false
	suite := eval.SWESuite{
		Name: "cli-timeout-suite",
		Tasks: []eval.SWETask{{
			ID:     "direct",
			Family: "direct_question",
			Prompt: "Explain routing.",
			Tags:   []string{"read-only"},
			Scenario: eval.Scenario{
				Name:                "direct",
				PromptContains:      []string{"Explain routing"},
				RequireVerification: "none",
				RequireApproval:     &requireApproval,
				RequireRoutePlan: &eval.RoutePlanExpectation{
					InitialLane: "main",
					Reason:      "direct_conversation",
					Contract:    "direct_answer",
				},
			},
			Oracle: eval.SWEOracle{
				RequireNoChangedFiles: true,
				RequireNoApproval:     true,
				MaxDurationMillis:     &maxDurationMillis,
			},
		}},
	}

	if err := writeSWESuite(suitePath, suite); err != nil {
		t.Fatalf("write suite: %v", err)
	}

	command := strings.Join([]string{
		`test "$IDIOM_SWE_TASK_TIMEOUT" = "75"`,
		`&& printf '%s\n'`,
		`'{"type":"message","role":"user","content":"Explain routing."}'`,
		`'{"type":"message","role":"route_plan","content":"initial=main reason=direct_conversation contract=direct_answer"}'`,
		`'{"type":"message","role":"assistant","content":"Routing is direct for this smoke test."}'`,
		`> {log_path}`,
	}, " ")

	var stdout, stderr bytes.Buffer

	code := run([]string{"-suite", suitePath, "-run-live", "-generated-logs", logsDir, "-runner-command", command}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("expected exit 0, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}

	if !strings.Contains(stderr.String(), "[1/1] running direct (timeout=75s)") {
		t.Fatalf("expected task start progress in stderr, got %s", stderr.String())
	}

	if !strings.Contains(stderr.String(), "[1/1] finished direct in ") {
		t.Fatalf("expected task finish progress in stderr, got %s", stderr.String())
	}
}

func TestTaskEnvEntriesFilterRunnerOwnedEnv(t *testing.T) {
	entries := taskEnvEntries(map[string]string{
		"IDIOM_SWE_LOG":    "/tmp/should-not-win.jsonl",
		"IDIOM_BIN":        "/tmp/idiom",
		"CUSTOM_ENV":       "visible",
		" IDIOM_SWE_TASK ": "ignored",
	})
	joined := strings.Join(entries, "\n")

	if strings.Contains(joined, "IDIOM_SWE_LOG=") || strings.Contains(joined, "IDIOM_BIN=") || strings.Contains(joined, "IDIOM_SWE_TASK=") {
		t.Fatalf("runner-owned env keys must be filtered, got %#v", entries)
	}

	if !strings.Contains(joined, "CUSTOM_ENV=visible") {
		t.Fatalf("custom env should remain, got %#v", entries)
	}
}

func TestLiveTaskTimeoutAllowsLongPlanExecuteAtoms(t *testing.T) {
	maxDurationMillis := int64(900000)
	got := liveTaskTimeoutSeconds(eval.SWETask{
		ID:     "medium-plan-execute",
		Family: "plan_then_execute_planner",
		Oracle: eval.SWEOracle{MaxDurationMillis: &maxDurationMillis},
	})

	if got != 930 {
		t.Fatalf("timeout = %d, want 930", got)
	}
}

func TestRunWritesFailingScenarioSuite(t *testing.T) {
	dir := t.TempDir()
	suitePath := filepath.Join(dir, "suite.json")
	logsDir := filepath.Join(dir, "logs")
	outPath := filepath.Join(dir, "regressions.json")

	if err := os.MkdirAll(logsDir, 0o755); err != nil {
		t.Fatalf("mkdir logs: %v", err)
	}

	writeSWESmokeSuite(t, suitePath, "")

	if err := os.WriteFile(filepath.Join(logsDir, "direct.jsonl"), []byte(`{"type":"message","role":"user","content":"Explain routing."}
{"type":"message","role":"assistant","content":"No route plan here."}
`), 0o644); err != nil {
		t.Fatalf("write failing log: %v", err)
	}

	var stdout, stderr bytes.Buffer

	code := run([]string{"-suite", suitePath, "-logs", logsDir, "-write-failing-scenarios", outPath}, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("expected failing eval exit 1, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read generated suite: %v", err)
	}

	if !strings.Contains(string(data), "direct-regression") || !strings.Contains(string(data), "generated-regression") {
		t.Fatalf("expected generated regression task, got %s", data)
	}
}

func TestRenderRunnerCommandDoesNotReplacePlaceholdersInsideValues(t *testing.T) {
	rendered := renderRunnerCommand(`printf '%s %s\n' {prompt} {log_path}`, map[string]string{
		"prompt":   "literal {log_path} in prompt",
		"log_path": "/tmp/out.jsonl",
	})
	if !strings.Contains(rendered, `'literal {log_path} in prompt'`) {
		t.Fatalf("expected prompt placeholder text to remain literal, got %s", rendered)
	}

	if !strings.Contains(rendered, `'/tmp/out.jsonl'`) {
		t.Fatalf("expected log path replacement, got %s", rendered)
	}

	rendered = renderRunnerCommand(`printf '%s\n' {turns_json}`, map[string]string{
		"turns_json": `["first turn","second turn"]`,
	})
	if !strings.Contains(rendered, `'["first turn","second turn"]'`) {
		t.Fatalf("expected turns_json replacement, got %s", rendered)
	}
}

func TestRunReportsMissingLogs(t *testing.T) {
	dir := t.TempDir()
	suitePath := filepath.Join(dir, "suite.json")
	writeSWESmokeSuite(t, suitePath, "")

	var stdout, stderr bytes.Buffer

	code := run([]string{"-suite", suitePath}, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("expected exit 1 for missing logs, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}

	if !strings.Contains(stdout.String(), "missing_logs=1") {
		t.Fatalf("expected missing log summary, got %s", stdout.String())
	}
}

func TestTaskLogPathUsesExplicitAndLogsDirPaths(t *testing.T) {
	task := smokeSWETask("")
	if got := taskLogPath("/tmp/suite.json", "/tmp/logs", task); got != filepath.Join("/tmp/logs", "direct.jsonl") {
		t.Fatalf("unexpected logs-dir path: %q", got)
	}

	task.LogPath = "custom/direct.jsonl"
	if got := taskLogPath("/tmp/suites/suite.json", "/tmp/logs", task); got != filepath.Clean("/tmp/suites/custom/direct.jsonl") {
		t.Fatalf("unexpected relative explicit path: %q", got)
	}

	task.LogPath = filepath.Join(string(filepath.Separator), "var", "tmp", "direct.jsonl")
	if got := taskLogPath("/tmp/suites/suite.json", "/tmp/logs", task); got != filepath.Clean(task.LogPath) {
		t.Fatalf("unexpected absolute explicit path: %q", got)
	}
}

func writeSWEMultiTurnSmokeSuite(t *testing.T, path string) {
	t.Helper()

	suite := `{
  "name": "cli-smoke-suite",
  "tasks": [{
    "id": "direct",
    "family": "direct_question",
    "prompt": "Explain routing.",
    "turns": ["first turn", "second turn"],
    "tags": ["read-only"],
    "scenario": {
      "name": "direct",
      "prompt_contains": ["second turn"],
      "require_assistant_for_each_user": true,
      "require_route_plan": {
        "initial_lane": "main",
        "reason": "direct_conversation",
        "contract": "direct_answer"
      },
      "require_verification": "none",
      "require_approval": false
    },
    "oracle": {
      "require_no_changed_files": true,
      "require_no_approval": true,
      "min_user_turns": 2,
      "min_assistant_turns": 2,
      "max_tool_requests": 2
    }
  }]
}
`
	if err := os.WriteFile(path, []byte(suite), 0o644); err != nil {
		t.Fatalf("write multi-turn suite: %v", err)
	}
}

func writeSWETwoTaskSmokeSuite(t *testing.T, path string) {
	t.Helper()

	suite := `{
  "name": "cli-two-task-suite",
  "tasks": [
    {
      "id": "first",
      "family": "direct_question",
      "prompt": "Explain routing.",
      "tags": ["read-only"],
      "scenario": {
        "name": "first",
        "prompt_contains": ["Explain routing"],
        "require_route_plan": {
          "initial_lane": "main",
          "reason": "direct_conversation",
          "contract": "direct_answer"
        },
        "require_verification": "none",
        "require_approval": false
      },
      "oracle": {
        "require_no_changed_files": true,
        "require_no_approval": true,
        "max_errors": 0
      }
    },
    {
      "id": "second",
      "family": "direct_question",
      "prompt": "Explain routing.",
      "tags": ["read-only"],
      "scenario": {
        "name": "second",
        "prompt_contains": ["Explain routing"],
        "require_route_plan": {
          "initial_lane": "main",
          "reason": "direct_conversation",
          "contract": "direct_answer"
        },
        "require_verification": "none",
        "require_approval": false
      },
      "oracle": {
        "require_no_changed_files": true,
        "require_no_approval": true,
        "max_tool_requests": 2
      }
    }
  ]
}
`
	if err := os.WriteFile(path, []byte(suite), 0o644); err != nil {
		t.Fatalf("write two-task suite: %v", err)
	}
}

func writeSWESmokeSuite(t *testing.T, path string, logPath string) {
	t.Helper()

	taskLog := ""
	if strings.TrimSpace(logPath) != "" {
		taskLog = `, "log_path": ` + quoteJSON(logPath)
	}

	suite := `{
  "name": "cli-smoke-suite",
  "tasks": [{
    "id": "direct",
    "family": "direct_question",
    "prompt": "Explain routing.",
    "tags": ["read-only"]` + taskLog + `,
    "scenario": {
      "name": "direct",
      "prompt_contains": ["Explain routing"],
      "require_route_plan": {
        "initial_lane": "main",
        "reason": "direct_conversation",
        "contract": "direct_answer"
      },
      "require_verification": "none",
      "require_approval": false
    },
    "oracle": {
      "require_no_changed_files": true,
      "require_no_approval": true,
      "max_tool_requests": 2
    }
  }]
}
`
	if err := os.WriteFile(path, []byte(suite), 0o644); err != nil {
		t.Fatalf("write suite: %v", err)
	}
}

func writeSWESmokeLog(t *testing.T, path string) {
	t.Helper()

	const log = `{"type":"message","role":"user","content":"Explain routing."}
{"type":"message","role":"route_plan","content":"initial=main reason=direct_conversation contract=direct_answer"}
{"type":"message","role":"control_rails","content":"{\"task_family\":\"conversation\",\"permission_profile\":\"conversation_only\"}"}
{"type":"message","role":"assistant","content":"Routing is direct for this smoke test."}
`

	if err := os.WriteFile(path, []byte(log), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}
}

func smokeSWETask(logPath string) eval.SWETask {
	return eval.SWETask{ID: "direct", LogPath: logPath}
}

func quoteJSON(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
}

// TestRepresentativeSuiteLintsClean is the per-PR drift sentinel: it runs the
// real testdata/swe/representative-suite.json through `-suite-only` so that
// coverage-contract violations (a required family lost its only task, a
// fixture path renamed, a tag dropped) fail `go test ./...` instead of waiting
// for a manual `make swe-suite` invocation.
func TestRepresentativeSuiteLintsClean(t *testing.T) {
	suitePath := filepath.Join(sweEvalTestRepoRoot(t), "testdata", "swe", "representative-suite.json")
	if _, err := os.Stat(suitePath); err != nil {
		t.Fatalf("representative suite missing — drift sentinel cannot run: %v", err)
	}

	var stdout, stderr bytes.Buffer

	code := run([]string{"-suite", suitePath, "-suite-only"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("representative suite failed -suite-only lint (exit %d).\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}

	if !strings.Contains(stdout.String(), "Lint: PASS") {
		t.Fatalf("expected `Lint: PASS` in suite report, got:\n%s", stdout.String())
	}
}

func TestCLIParityRegressionSuiteLintsClean(t *testing.T) {
	suitePath := filepath.Join(sweEvalTestRepoRoot(t), "testdata", "swe", "cli-parity-regression-suite.json")
	if _, err := os.Stat(suitePath); err != nil {
		t.Fatalf("cli parity regression suite missing: %v", err)
	}

	var stdout, stderr bytes.Buffer

	code := run([]string{"-suite", suitePath, "-suite-only"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("cli parity regression suite failed -suite-only lint (exit %d).\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}

	if !strings.Contains(stdout.String(), "Lint: PASS") {
		t.Fatalf("expected `Lint: PASS` in suite report, got:\n%s", stdout.String())
	}
}

func TestLiveContractSuiteLintsClean(t *testing.T) {
	suitePath := filepath.Join(sweEvalTestRepoRoot(t), "testdata", "live-contracts", "suite.json")
	if _, err := os.Stat(suitePath); err != nil {
		t.Fatalf("live contract suite missing: %v", err)
	}

	var stdout, stderr bytes.Buffer

	code := run([]string{"-suite", suitePath, "-suite-only"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("live contract suite failed -suite-only lint (exit %d).\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}

	if !strings.Contains(stdout.String(), "Lint: PASS") {
		t.Fatalf("expected `Lint: PASS` in suite report, got:\n%s", stdout.String())
	}
}

func TestScriptedContractSuiteLintsClean(t *testing.T) {
	suitePath := filepath.Join(sweEvalTestRepoRoot(t), "testdata", "scripted-contracts", "suite.json")
	if _, err := os.Stat(suitePath); err != nil {
		t.Fatalf("scripted contract suite missing: %v", err)
	}

	var stdout, stderr bytes.Buffer

	code := run([]string{"-suite", suitePath, "-suite-only"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("scripted contract suite failed -suite-only lint (exit %d).\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}

	if !strings.Contains(stdout.String(), "Lint: PASS") {
		t.Fatalf("expected `Lint: PASS` in suite report, got:\n%s", stdout.String())
	}
}

func TestScriptedContractGenericHarnessClassTasksPinned(t *testing.T) {
	suitePath := filepath.Join(sweEvalTestRepoRoot(t), "testdata", "scripted-contracts", "suite.json")

	suite, err := eval.LoadSWESuite(suitePath)
	if err != nil {
		t.Fatalf("load scripted contract suite: %v", err)
	}

	taskByID := map[string]eval.SWETask{}
	for _, task := range suite.Tasks {
		taskByID[task.ID] = task
	}

	chat, ok := taskByID["scripted-chat-no-workspace-tools"]
	if !ok {
		t.Fatalf("generic no-tool chat task missing")
	}

	assertTaskHasTags(t, chat, "chat", "no-tool", "read-only", "rollback")
	assertTaskHasRunEvents(t, chat,
		"idiom.live_contract.workspace_prepared",
		"idiom.live_contract.resolved_config",
		"idiom.live_contract.rollback",
		"idiom.scripted_model.requests",
	)

	if chat.Family != "general_chat" || chat.Oracle.MaxToolRequests == nil || *chat.Oracle.MaxToolRequests != 0 || !chat.Oracle.RequireNoChangedFiles {
		t.Fatalf("generic chat task must assert zero workspace-tool use and no mutation, family=%q oracle=%#v", chat.Family, chat.Oracle)
	}

	for _, forbidden := range []string{"read_file", "shell", "edit_file", "submit_plan"} {
		if !slices.Contains(chat.Scenario.ForbidTools, forbidden) {
			t.Fatalf("generic chat task must forbid %q, got %#v", forbidden, chat.Scenario.ForbidTools)
		}
	}

	deniedRead, ok := taskByID["scripted-workspace-tool-rules-deny-read-file"]
	if !ok {
		t.Fatalf("workspace tool_rules deny read_file task missing")
	}

	assertTaskHasTags(t, deniedRead, "information", "tool-rules", "typed-blocker", "read-only", "rollback")
	assertTaskHasRunEvents(t, deniedRead, "permission_decision", "tool_policy_result", "completion_status")

	if deniedRead.Family != "information_request" {
		t.Fatalf("workspace tool_rules deny task must be an information request, got %q", deniedRead.Family)
	}

	if !strings.Contains(deniedRead.Env["IDIOM_WORKSPACE_TOOL_RULES_JSON"], `"decision":"deny"`) ||
		!strings.Contains(deniedRead.Env["IDIOM_WORKSPACE_EXTRA_FILES_JSON"], "SECRET_SENTINEL_DO_NOT_LEAK") {

		t.Fatalf("workspace tool_rules deny task must configure a denied sentinel file, env=%#v", deniedRead.Env)
	}

	if !slices.Contains(deniedRead.Scenario.RequireTools, "read_file") ||
		!slices.Contains(deniedRead.Scenario.ForbidTools, "completion_gate") {

		t.Fatalf("workspace tool_rules deny task must require one read and forbid completion-gate recovery, scenario=%#v", deniedRead.Scenario)
	}

	if !slices.Contains(deniedRead.Scenario.ForbidFinalAssistantContains, "SECRET_SENTINEL_DO_NOT_LEAK") {
		t.Fatalf("workspace tool_rules deny task must forbid sentinel leakage, scenario=%#v", deniedRead.Scenario)
	}

	if deniedRead.Oracle.MaxToolRequests == nil || *deniedRead.Oracle.MaxToolRequests != 1 ||
		deniedRead.Oracle.MaxErrors == nil || *deniedRead.Oracle.MaxErrors != 1 ||
		!deniedRead.Oracle.RequireNoChangedFiles {

		t.Fatalf("workspace tool_rules deny task must pin one denied tool request and no mutation, oracle=%#v", deniedRead.Oracle)
	}

	info, ok := taskByID["scripted-information-readme-grounded-answer"]
	if !ok {
		t.Fatalf("grounded information task missing")
	}

	assertTaskHasTags(t, info, "information", "grounded-answer", "read-only", "rollback")

	if info.Family != "information_request" || info.Oracle.Groundedness == nil || !slices.Contains(info.Scenario.RequireEvidencePaths, "README.md") {
		t.Fatalf("information task must require README evidence and groundedness, family=%q scenario=%#v oracle=%#v", info.Family, info.Scenario, info.Oracle)
	}

	if !slices.Contains(info.Scenario.RequireTools, "read_file") || slices.Contains(info.Scenario.ForbidTools, "read_file") {
		t.Fatalf("information task must allow exactly source-backed read behavior, scenario=%#v", info.Scenario)
	}

	help, ok := taskByID["scripted-coding-help-test-advice-read-only"]
	if !ok {
		t.Fatalf("read-only coding-help task missing")
	}

	assertTaskHasTags(t, help, "coding-help", "grounded-answer", "read-only", "rollback")

	if help.Oracle.Groundedness == nil {
		t.Fatalf("coding-help task must require groundedness, oracle=%#v", help.Oracle)
	}

	for _, want := range []string{"internal/labels/labels.go", "internal/labels/labels_test.go"} {
		if !slices.Contains(help.Scenario.RequireEvidencePaths, want) {
			t.Fatalf("coding-help task must require grounded evidence path %q, scenario=%#v oracle=%#v", want, help.Scenario, help.Oracle)
		}
	}

	if len(help.Oracle.Groundedness.ContextFragments) == 0 {
		t.Fatalf("coding-help task must include fixture-local groundedness fragments, oracle=%#v", help.Oracle)
	}

	if help.Family != "coding_help" || help.Scenario.RequireVerification != "none" || !help.Oracle.RequireNoShell || !help.Oracle.RequireNoChangedFiles {
		t.Fatalf("coding-help task must stay read-only, family=%q scenario=%#v oracle=%#v", help.Family, help.Scenario, help.Oracle)
	}
}

func TestScriptedContractReplayCoverageTasksPinned(t *testing.T) {
	suitePath := filepath.Join(sweEvalTestRepoRoot(t), "testdata", "scripted-contracts", "suite.json")

	suite, err := eval.LoadSWESuite(suitePath)
	if err != nil {
		t.Fatalf("load scripted contract suite: %v", err)
	}

	taskByID := map[string]eval.SWETask{}
	for _, task := range suite.Tasks {
		taskByID[task.ID] = task
	}

	blocked, ok := taskByID["scripted-contract-replay-blocked-post-write-completion"]
	if !ok {
		t.Fatalf("blocked contract replay task missing")
	}

	assertTaskHasTags(t, blocked, "contract-replay", "gate-decision", "post-write-verification", "recovery")
	assertTaskHasRunEvents(t, blocked, "autonomy_event", "post_write_verification_required", "completion_gate_recovery")

	if blocked.Scenario.RequireVerification != "passed" || blocked.Oracle.MaxErrors == nil || *blocked.Oracle.MaxErrors != 1 {
		t.Fatalf("blocked replay task must recover after one gate rejection, scenario=%#v oracle=%#v", blocked.Scenario, blocked.Oracle)
	}

	valid, ok := taskByID["scripted-contract-replay-valid-post-write-verification"]
	if !ok {
		t.Fatalf("valid contract replay task missing")
	}

	assertTaskHasTags(t, valid, "contract-replay", "gate-decision", "post-write-verification")
	assertTaskHasRunEvents(t, valid, "autonomy_event", "post_write_verification_required")

	if slices.Contains(valid.Scenario.RequireRunEvents, "completion_gate_recovery") {
		t.Fatalf("valid replay task should not require a completion-gate recovery event")
	}

	if valid.Scenario.RequireVerification != "passed" || !valid.Oracle.RequireNoRunnerRejections {
		t.Fatalf("valid replay task must require clean post-write verification, scenario=%#v oracle=%#v", valid.Scenario, valid.Oracle)
	}
}

func TestScriptedContractTDDExpansionTasksExerciseSuiteBehavior(t *testing.T) {
	suitePath := filepath.Join(sweEvalTestRepoRoot(t), "testdata", "scripted-contracts", "suite.json")

	suite, err := eval.LoadSWESuite(suitePath)
	if err != nil {
		t.Fatalf("load scripted contract suite: %v", err)
	}

	taskByID := map[string]eval.SWETask{}
	for _, task := range suite.Tasks {
		taskByID[task.ID] = task
	}

	for _, tc := range []struct {
		id   string
		tag  string
		atom string
		tdd  string
		sdd  string
	}{
		{
			id:   "scripted-feature-planner-tui-tdd-state-machine-atom",
			tag:  "state-machine",
			atom: "atom-tdd-state-machine-coverage",
			tdd:  "TDD-StateMachineCoverage",
			sdd:  "SDD-TDD-StateMachineCoverage",
		},
		{
			id:   "scripted-feature-planner-tui-tdd-boundary-value-atom",
			tag:  "boundary-value",
			atom: "atom-tdd-boundary-value-coverage",
			tdd:  "TDD-BoundaryValueCoverage",
			sdd:  "SDD-TDD-BoundaryValueCoverage",
		},
		{
			id:   "scripted-feature-planner-tui-tdd-fault-tree-atom",
			tag:  "fault-tree",
			atom: "atom-tdd-fault-tree-coverage",
			tdd:  "TDD-FaultTreeCoverage",
			sdd:  "SDD-TDD-FaultTreeCoverage",
		},
		{
			id:   "scripted-feature-planner-tui-tdd-post-mortem-atom",
			tag:  "post-mortem",
			atom: "atom-tdd-post-mortem-coverage",
			tdd:  "TDD-PostMortemCoverage",
			sdd:  "SDD-TDD-PostMortemCoverage",
		},
		{
			id:   "scripted-feature-planner-tui-tdd-formal-verification-atom",
			tag:  "formal-verification",
			atom: "atom-tdd-formal-verification-coverage",
			tdd:  "TDD-FormalVerificationCoverage",
			sdd:  "SDD-TDD-FormalVerificationCoverage",
		},
	} {
		task, ok := taskByID[tc.id]
		if !ok {
			t.Fatalf("%s task missing", tc.id)
		}

		assertTaskHasTags(t, task, "feature-planner", "tui", "tdd", "sdd", "slash-command", tc.tag)
		assertTaskHasRunEvents(t, task,
			"idiom.feature_planner.start",
			"idiom.feature_planner.tdd_pre_execution_checked",
			"idiom.feature_planner.plan_accepted",
			"idiom.feature_planner.atom_dispatched",
			"internal_idiom.plan_execute.completed",
			"idiom.feature_planner.atom_completed",
			"idiom.feature_planner.complete",
			"idiom.live_contract.rollback",
		)

		for _, want := range []string{"feature-planner: complete", tc.atom, tc.tdd, tc.sdd} {
			if !slices.Contains(task.Scenario.FinalAssistantContains, want) {
				t.Fatalf("%s final assistant assertions must include %q, got %#v", tc.id, want, task.Scenario.FinalAssistantContains)
			}
		}

		if task.Scenario.RequireVerification != "none" || !task.Oracle.RequireCleanRollback || !task.Oracle.RequireNoChangedFiles {
			t.Fatalf("%s must be a no-edit TUI regression with clean rollback oracle, scenario=%#v oracle=%#v", tc.id, task.Scenario, task.Oracle)
		}
	}
}

func TestScriptedContractRedisCloneTestbedTaskExercisesRealFixture(t *testing.T) {
	suitePath := filepath.Join(sweEvalTestRepoRoot(t), "testdata", "scripted-contracts", "suite.json")

	suite, err := eval.LoadSWESuite(suitePath)
	if err != nil {
		t.Fatalf("load scripted contract suite: %v", err)
	}

	var task eval.SWETask

	for _, candidate := range suite.Tasks {
		if candidate.ID == "scripted-feature-planner-tui-redis-clone-mget" {
			task = candidate
			break
		}
	}

	if task.ID == "" {
		t.Fatal("redis clone feature-planner testbed task missing")
	}

	if task.Fixture != "fixtures/redis-clone-repo" {
		t.Fatalf("redis clone task fixture = %q, want fixtures/redis-clone-repo", task.Fixture)
	}

	assertTaskHasTags(t, task, "feature-planner", "tui", "tdd", "sdd", "slash-command", "plan-execute", "redis-clone", "large-testbed")
	assertTaskHasRunEvents(t, task,
		"idiom.feature_planner.start",
		"idiom.feature_planner.tdd_pre_execution_checked",
		"idiom.feature_planner.plan_accepted",
		"idiom.feature_planner.atom_dispatched",
		"internal_idiom.plan_execute.completed",
		"idiom.feature_planner.atom_completed",
		"idiom.feature_planner.complete",
		"idiom.live_contract.rollback",
	)

	for _, want := range []string{"feature-planner: complete", "atom-redis-clone-mget", "TDD-RedisCloneMGET", "SDD-RedisCloneMGET"} {
		if !slices.Contains(task.Scenario.FinalAssistantContains, want) {
			t.Fatalf("redis clone task final assistant assertions must include %q, got %#v", want, task.Scenario.FinalAssistantContains)
		}
	}

	for _, want := range []string{"internal/server/server.go", "internal/server/server_test.go"} {
		if !slices.Contains(task.Scenario.RequireChangedFiles, want) || !slices.Contains(task.Oracle.RequireChangedFiles, want) {
			t.Fatalf("redis clone task must require changed file %q, scenario=%#v oracle=%#v", want, task.Scenario.RequireChangedFiles, task.Oracle.RequireChangedFiles)
		}
	}

	if task.Scenario.RequireVerification != "none" || !task.Oracle.RequireCleanRollback || task.Oracle.RequireNoChangedFiles {
		t.Fatalf("redis clone task must be a rollback-scored TUI mutation regression, scenario=%#v oracle=%#v", task.Scenario, task.Oracle)
	}
}

func TestScriptedContractFeaturePlannerDirectAndProductWorkflowCoverageSeparated(t *testing.T) {
	suitePath := filepath.Join(sweEvalTestRepoRoot(t), "testdata", "scripted-contracts", "suite.json")

	suite, err := eval.LoadSWESuite(suitePath)
	if err != nil {
		t.Fatalf("load scripted contract suite: %v", err)
	}

	taskByID := map[string]eval.SWETask{}
	for _, task := range suite.Tasks {
		taskByID[task.ID] = task
	}

	if _, ok := taskByID["scripted-feature-planner-tui-product-workflow-e2e"]; ok {
		t.Fatal("plain /feature-planner product-workflow coverage task should be relabeled as direct plan-execute coverage")
	}

	direct, ok := taskByID["scripted-feature-planner-tui-direct-greeting-plan-execute"]
	if !ok {
		t.Fatal("direct /feature-planner nested plan-execute task missing")
	}

	assertTaskHasTags(t, direct, "feature-planner", "tui", "slash-command", "plan-execute")
	assertTaskLacksTags(t, direct, "product-workflow", "prd", "adr")

	if !strings.HasPrefix(direct.Prompt, "/feature-planner ") || !strings.Contains(direct.Prompt, "nested plan-execute") {
		t.Fatalf("direct task prompt should remain plain /feature-planner nested plan-execute coverage, got %q", direct.Prompt)
	}

	assertTaskHasRunEvents(t, direct,
		"idiom.feature_planner.start",
		"idiom.feature_planner.atom_dispatched",
		"internal_idiom.plan_execute.completed",
		"idiom.feature_planner.complete",
	)

	for _, want := range []string{"atom-direct-greeting-package", "TDD-DirectGreetingHello", "SDD-DirectGreetingPackage"} {
		if !slices.Contains(direct.Scenario.FinalAssistantContains, want) {
			t.Fatalf("direct feature-planner task final assistant assertions must include %q, got %#v", want, direct.Scenario.FinalAssistantContains)
		}
	}

	prd, ok := taskByID["scripted-product-create-tui-prd-workflow"]
	if !ok {
		t.Fatal("explicit /product-create PRD workflow task missing")
	}

	assertTaskHasTags(t, prd, "product-workflow", "tui", "prd", "slash-command")
	assertTaskLacksTags(t, prd, "feature-planner", "plan-execute", "adr")

	if !strings.HasPrefix(prd.Prompt, "/product-create ") {
		t.Fatalf("PRD workflow coverage must use /product-create, got %q", prd.Prompt)
	}

	adr, ok := taskByID["scripted-product-approve-tui-adr-workflow"]
	if !ok {
		t.Fatal("explicit /product-approve ADR workflow task missing")
	}

	assertTaskHasTags(t, adr, "product-workflow", "tui", "prd", "adr", "slash-command")
	assertTaskLacksTags(t, adr, "feature-planner", "plan-execute")

	if !strings.HasPrefix(adr.Prompt, "/product-approve ") {
		t.Fatalf("ADR workflow coverage must use /product-approve, got %q", adr.Prompt)
	}
}

func TestScriptedContractImplementNewFileCoveragePinned(t *testing.T) {
	suitePath := filepath.Join(sweEvalTestRepoRoot(t), "testdata", "scripted-contracts", "suite.json")

	suite, err := eval.LoadSWESuite(suitePath)
	if err != nil {
		t.Fatalf("load scripted contract suite: %v", err)
	}

	taskByID := make(map[string]eval.SWETask, len(suite.Tasks))
	for _, candidate := range suite.Tasks {
		taskByID[candidate.ID] = candidate
	}

	task := taskByID["scripted-implement-new-md-file-no-history"]
	if task.ID == "" {
		t.Fatal("implement new markdown file task missing")
	}

	assertTaskHasTags(t, task, "implement", "new-file", "rollback")
	assertTaskHasScenarioTools(t, task, "write_file")
	assertTaskHasRunEvents(t, task,
		"idiom.live_contract.workspace_prepared",
		"idiom.live_contract.resolved_config",
		"idiom.live_contract.rollback",
		"idiom.scripted_model.requests",
	)

	for _, forbidden := range []string{"read_file", "history_range", "history_symbol", "blame_range", "source_sanity", "shell"} {
		if !slices.Contains(task.Scenario.ForbidTools, forbidden) {
			t.Fatalf("implement new-file task must forbid %s, scenario=%#v", forbidden, task.Scenario)
		}
	}

	if !slices.Contains(task.Scenario.RequireChangedFiles, "temp.md") ||
		!slices.Contains(task.Oracle.RequireChangedFiles, "temp.md") {
		t.Fatalf("implement new-file task must require temp.md change, scenario=%#v oracle=%#v", task.Scenario.RequireChangedFiles, task.Oracle.RequireChangedFiles)
	}

	if len(task.Oracle.RequireFileContent) != 1 ||
		task.Oracle.RequireFileContent[0].Path != "temp.md" ||
		!slices.Contains(task.Oracle.RequireFileContent[0].Contains, "temp") {
		t.Fatalf("implement new-file task must pin temp.md content oracle, oracle=%#v", task.Oracle.RequireFileContent)
	}

	if task.Oracle.RequireNoChangedFiles || !task.Oracle.RequireCleanRollback || !task.Oracle.RequireNoShell {
		t.Fatalf("implement new-file task must be rollback-scored no-shell coverage, oracle=%#v", task.Oracle)
	}
}

func TestScriptedContractPlanExecuteNewFileCoveragePinned(t *testing.T) {
	suitePath := filepath.Join(sweEvalTestRepoRoot(t), "testdata", "scripted-contracts", "suite.json")

	suite, err := eval.LoadSWESuite(suitePath)
	if err != nil {
		t.Fatalf("load scripted contract suite: %v", err)
	}

	taskByID := make(map[string]eval.SWETask, len(suite.Tasks))
	for _, candidate := range suite.Tasks {
		taskByID[candidate.ID] = candidate
	}

	task := taskByID["scripted-plan-execute-authorized-new-file"]
	if task.ID == "" {
		t.Fatal("plan-execute authorized new-file task missing")
	}

	assertTaskHasTags(t, task, "plan-execute", "new-file", "rollback", "go")
	assertTaskHasRunEvents(t, task,
		"idiom.plan_execute.required_edit_paths_resolved",
		"idiom.plan_execute.plan_artifact_loaded",
		"idiom.plan_execute.executor_handoff_packet",
		"idiom.plan_execute.preflight_ok",
		"idiom.plan_execute.executor_complete",
	)

	if !strings.Contains(task.Env["IDIOM_EXTRA_ARGS"], "-authorized-new-file internal/labels/generated_label.go") {
		t.Fatalf("new-file task must pass typed new-file authorization, env=%#v", task.Env)
	}
	if task.Env["IDIOM_LIVE_SNAPSHOT_PATHS_JSON"] != "[\"internal/labels/generated_label.go\"]" {
		t.Fatalf("new-file task must snapshot generated file before rollback, env=%#v", task.Env)
	}

	if !slices.Contains(task.Scenario.RequireChangedFiles, "internal/labels/generated_label.go") ||
		!slices.Contains(task.Scenario.RequireChangedFiles, "internal/labels/labels.go") ||
		!slices.Contains(task.Oracle.RequireChangedFiles, "internal/labels/generated_label.go") ||
		!slices.Contains(task.Oracle.RequireChangedFiles, "internal/labels/labels.go") {
		t.Fatalf("new-file task must require generated label and baseline label changes, scenario=%#v oracle=%#v", task.Scenario.RequireChangedFiles, task.Oracle.RequireChangedFiles)
	}

	if len(task.Oracle.RequireFileContent) != 1 ||
		task.Oracle.RequireFileContent[0].Path != "internal/labels/generated_label.go" ||
		!slices.Contains(task.Oracle.RequireFileContent[0].Contains, "func GeneratedLabel() string") {
		t.Fatalf("new-file task must pin generated file content oracle, oracle=%#v", task.Oracle.RequireFileContent)
	}
	if slices.Contains(task.Oracle.ForbidChangedFiles, "internal/labels/labels.go") {
		t.Fatalf("new-file task must allow the baseline label fix required for verification, oracle=%#v", task.Oracle)
	}

	if task.Oracle.RequireNoChangedFiles || !task.Oracle.RequireCleanRollback || !task.Oracle.RequireExecutorHandoff {
		t.Fatalf("new-file task must be rollback-scored executor coverage, oracle=%#v", task.Oracle)
	}

	only := taskByID["scripted-plan-execute-authorized-new-file-only"]
	if only.ID == "" {
		t.Fatal("plan-execute authorized new-file-only task missing")
	}
	assertTaskHasTags(t, only, "plan-execute", "new-file", "rollback", "go")
	if !strings.Contains(only.Env["IDIOM_EXTRA_ARGS"], "-authorized-new-file internal/labels/generated_only.go") {
		t.Fatalf("new-file-only task must pass typed new-file authorization, env=%#v", only.Env)
	}
	assertTaskHasScenarioTools(t, only, "submit_plan")
	for _, forbidden := range []string{"read_file", "history_range", "history_symbol"} {
		if !slices.Contains(only.Scenario.ForbidTools, forbidden) {
			t.Fatalf("new-file-only task must forbid %s before submit_plan, scenario=%#v", forbidden, only.Scenario)
		}
	}
	if len(only.Oracle.RequireFileContent) != 1 ||
		only.Oracle.RequireFileContent[0].Path != "internal/labels/generated_only.go" ||
		!slices.Contains(only.Oracle.RequireFileContent[0].Contains, "func GeneratedOnly() string") {
		t.Fatalf("new-file-only task must pin generated-only file content oracle, oracle=%#v", only.Oracle.RequireFileContent)
	}
	if !slices.Contains(only.Oracle.ForbidChangedFiles, "internal/labels/labels.go") {
		t.Fatalf("new-file-only task must prove existing files stay untouched, oracle=%#v", only.Oracle)
	}

	unspecified := taskByID["scripted-plan-execute-unspecified-new-file-blocker"]
	if unspecified.ID == "" {
		t.Fatal("plan-execute unspecified new-file blocker task missing")
	}
	assertTaskHasTags(t, unspecified, "plan-execute", "new-file", "approval-required", "closed-scope", "typed-blocker")
	if !unspecified.Oracle.RequireNoChangedFiles || !unspecified.Oracle.AllowNoWrite {
		t.Fatalf("unspecified new-file task must be no-write blocker coverage, oracle=%#v", unspecified.Oracle)
	}
	for _, forbidden := range []string{"submit_plan", "edit_file", "write_file", "append_file", "apply_patch"} {
		if !slices.Contains(unspecified.Scenario.ForbidTools, forbidden) {
			t.Fatalf("unspecified new-file task must forbid %s, scenario=%#v", forbidden, unspecified.Scenario)
		}
	}

	reference := taskByID["scripted-plan-execute-reference-readonly-closed-scope"]
	if reference.ID == "" {
		t.Fatal("plan-execute reference-readonly closed-scope task missing")
	}
	assertTaskHasTags(t, reference, "plan-execute", "closed-scope", "rollback", "go")
	assertTaskHasScenarioTools(t, reference, "read_file", "submit_plan")
	if !slices.Contains(reference.Oracle.ForbidChangedFiles, "README.md") ||
		!slices.Contains(reference.Oracle.ForbidChangedFiles, "internal/labels/labels_test.go") {
		t.Fatalf("reference-readonly task must forbid reference/test changes, oracle=%#v", reference.Oracle)
	}
	if !slices.Contains(reference.Scenario.RequireChangedFiles, "internal/labels/labels.go") ||
		!slices.Contains(reference.Oracle.RequireChangedFiles, "internal/labels/labels.go") {
		t.Fatalf("reference-readonly task must require only label source change, scenario=%#v oracle=%#v", reference.Scenario, reference.Oracle)
	}
}

func assertTaskHasTags(t *testing.T, task eval.SWETask, tags ...string) {
	t.Helper()

	for _, tag := range tags {
		if !slices.Contains(task.Tags, tag) {
			t.Fatalf("%s missing tag %q in %#v", task.ID, tag, task.Tags)
		}
	}
}

func assertTaskHasScenarioTools(t *testing.T, task eval.SWETask, tools ...string) {
	t.Helper()

	for _, tool := range tools {
		if !slices.Contains(task.Scenario.RequireTools, tool) {
			t.Fatalf("task %s missing required tool %q: %#v", task.ID, tool, task.Scenario.RequireTools)
		}
	}
}

func assertTaskLacksTags(t *testing.T, task eval.SWETask, tags ...string) {
	t.Helper()

	for _, tag := range tags {
		if slices.Contains(task.Tags, tag) {
			t.Fatalf("%s should not have tag %q in %#v", task.ID, tag, task.Tags)
		}
	}
}

func assertTaskHasRunEvents(t *testing.T, task eval.SWETask, events ...string) {
	t.Helper()

	for _, event := range events {
		if !slices.Contains(task.Scenario.RequireRunEvents, event) {
			t.Fatalf("%s missing required run event %q in %#v", task.ID, event, task.Scenario.RequireRunEvents)
		}
	}
}
