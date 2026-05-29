package sweeval
import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"idiom/internal/eval"
)

type liveSuiteRunner struct {
	suitePath       string
	outputDir       string
	commandTemplate string
	progress        io.Writer
	continueOnError bool
}

func runLiveSuiteTasks(suite eval.SWESuite, suitePath string, outputDir string, commandTemplate string, stderr io.Writer, continueOnError bool) (map[string]string, error) {
	runner := liveSuiteRunner{
		suitePath:       suitePath,
		outputDir:       outputDir,
		commandTemplate: strings.TrimSpace(commandTemplate),
		progress:        stderr,
		continueOnError: continueOnError,
	}
	return runner.run(suite)
}

func (r liveSuiteRunner) run(suite eval.SWESuite) (map[string]string, error) {
	if r.commandTemplate == "" {
		return nil, fmt.Errorf("-run-live requires -runner-command")
	}
	absOutputDir, err := filepath.Abs(r.outputDir)
	if err != nil {
		return nil, fmt.Errorf("resolve generated logs dir: %w", err)
	}
	if err := os.MkdirAll(absOutputDir, 0o755); err != nil {
		return nil, fmt.Errorf("create generated logs dir: %w", err)
	}
	r.outputDir = absOutputDir

	logPaths := make(map[string]string, len(suite.Tasks))
	for i, task := range suite.Tasks {
		logPath, err := r.prepareTaskLogPath(task)
		if err != nil {
			return nil, err
		}
		logPaths[task.ID] = logPath
		if err := r.runTask(i, len(suite.Tasks), task, logPath); err != nil {
			if r.continueOnError {
				if writeErr := r.ensureRunnerErrorLog(task, logPath, err); writeErr != nil {
					return nil, writeErr
				}
				fmt.Fprintf(r.progress, "[%d/%d] failed %s: %v\n", i+1, len(suite.Tasks), strings.TrimSpace(task.ID), err)
				continue
			}
			return nil, err
		}
	}
	return logPaths, nil
}

func (r liveSuiteRunner) prepareTaskLogPath(task eval.SWETask) (string, error) {
	logPath := taskLogPath(r.suitePath, r.outputDir, task)
	if strings.TrimSpace(task.LogPath) != "" {
		logPath = filepath.Join(r.outputDir, strings.TrimSpace(task.ID)+".jsonl")
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return "", fmt.Errorf("create log dir for %s: %w", task.ID, err)
	}
	_ = os.Remove(logPath)
	return logPath, nil
}

func (r liveSuiteRunner) runTask(index int, total int, task eval.SWETask, logPath string) error {
	fixture := taskFixturePath(r.suitePath, task)
	if fixture != "" {
		if abs, err := filepath.Abs(fixture); err == nil {
			fixture = abs
		}
	}
	prompt := taskPrompt(task)
	turns := taskTurns(task)
	turnsJSON, err := json.Marshal(turns)
	if err != nil {
		return fmt.Errorf("encode turns for %s: %w", task.ID, err)
	}
	command := renderRunnerCommand(r.commandTemplate, map[string]string{
		"prompt":      prompt,
		"turns_json":  string(turnsJSON),
		"turn_count":  fmt.Sprintf("%d", len(turns)),
		"task_id":     strings.TrimSpace(task.ID),
		"runner_mode": strings.TrimSpace(task.RunnerMode),
		"log_path":    logPath,
		"fixture":     fixture,
	})
	taskTimeout := liveTaskTimeoutSeconds(task)
	start := time.Now()
	fmt.Fprintf(r.progress, "[%d/%d] running %s (timeout=%ds)\n", index+1, total, strings.TrimSpace(task.ID), taskTimeout)
	cmdCtx, cancel := context.WithTimeout(context.Background(), time.Duration(taskTimeout+45)*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cmdCtx, "/bin/sh", "-lc", command)
	cmd.Dir = firstNonEmpty(fixture, filepath.Dir(r.suitePath))
	cmd.Env = append(os.Environ(), taskEnvEntries(task.Env)...)
	cmd.Env = append(cmd.Env,
		"IDIOM_SWE_PROMPT="+prompt,
		"IDIOM_SWE_TURNS_JSON="+string(turnsJSON),
		"IDIOM_SWE_TURN_COUNT="+fmt.Sprintf("%d", len(turns)),
		"IDIOM_SWE_TASK_ID="+strings.TrimSpace(task.ID),
		"IDIOM_SWE_FAMILY="+strings.TrimSpace(task.Family),
		"IDIOM_SWE_RUNNER_MODE="+strings.TrimSpace(task.RunnerMode),
		"IDIOM_SWE_LOG="+logPath,
		"IDIOM_SWE_FIXTURE="+fixture,
		"IDIOM_SWE_FIXTURE_GIT_REF="+strings.TrimSpace(task.FixtureGitRef),
		"IDIOM_SWE_FIXTURE_GIT_REPO="+r.suiteGitRoot(),
		"IDIOM_SWE_TASK_TIMEOUT="+strconv.Itoa(taskTimeout),
	)
	output, err := cmd.CombinedOutput()
	if len(output) > 0 && fileMissingOrEmpty(logPath) {
		if writeErr := os.WriteFile(logPath, output, 0o644); writeErr != nil {
			return fmt.Errorf("write captured log for %s: %w", task.ID, writeErr)
		}
	}
	if err != nil {
		if _, loadErr := eval.LoadRecords(logPath); loadErr == nil && !fileMissingOrEmpty(logPath) {
			fmt.Fprintf(r.progress, "[%d/%d] runner exited nonzero for %s but wrote a scoreable log: %v\n", index+1, total, strings.TrimSpace(task.ID), err)
			fmt.Fprintf(r.progress, "[%d/%d] finished %s in %s -> %s\n", index+1, total, strings.TrimSpace(task.ID), time.Since(start).Round(time.Millisecond), logPath)
			return nil
		}
		if cmdCtx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("run live task %s: evaluator deadline exceeded after %ds", task.ID, taskTimeout+45)
		}
		return fmt.Errorf("run live task %s: %w: %s", task.ID, err, strings.TrimSpace(string(output)))
	}
	fmt.Fprintf(r.progress, "[%d/%d] finished %s in %s -> %s\n", index+1, total, strings.TrimSpace(task.ID), time.Since(start).Round(time.Millisecond), logPath)
	return nil
}

func (r liveSuiteRunner) suiteGitRoot() string {
	dir := filepath.Dir(r.suitePath)
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return dir
	}
	return strings.TrimSpace(string(out))
}

func (r liveSuiteRunner) ensureRunnerErrorLog(task eval.SWETask, logPath string, runErr error) error {
	if strings.TrimSpace(logPath) == "" {
		return nil
	}
	if _, err := eval.LoadRecords(logPath); err == nil && !fileMissingOrEmpty(logPath) {
		return nil
	}
	record := map[string]any{
		"type":    "error",
		"message": fmt.Sprintf("live runner task failed: %v", runErr),
		"task_id": strings.TrimSpace(task.ID),
		"source":  "idiom-swe-eval",
	}
	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode runner error for %s: %w", task.ID, err)
	}
	if err := os.WriteFile(logPath, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write runner error log for %s: %w", task.ID, err)
	}
	return nil
}

func liveTaskTimeoutSeconds(task eval.SWETask) int {
	if raw := strings.TrimSpace(os.Getenv("IDIOM_SWE_TASK_TIMEOUT")); raw != "" {
		if secs, err := strconv.Atoi(raw); err == nil && secs > 0 {
			return secs
		}
	}
	if task.Oracle.MaxDurationMillis != nil && *task.Oracle.MaxDurationMillis > 0 {
		secs := int((*task.Oracle.MaxDurationMillis + 999) / 1000)
		secs += 30
		if secs < 60 {
			secs = 60
		}
		return secs
	}
	return 180
}

func renderRunnerCommand(template string, values map[string]string) string {
	pairs := make([]string, 0, 8)
	for _, key := range []string{"prompt", "turns_json", "turn_count", "task_id", "runner_mode", "log_path", "fixture"} {
		if value, ok := values[key]; ok {
			pairs = append(pairs, "{"+key+"}", shellQuote(value))
		}
	}
	if len(pairs) == 0 {
		return template
	}
	return strings.NewReplacer(pairs...).Replace(template)
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func taskPrompt(task eval.SWETask) string {
	if strings.TrimSpace(task.Prompt) != "" {
		return strings.TrimSpace(task.Prompt)
	}
	if len(task.Turns) == 0 {
		return ""
	}
	return strings.TrimSpace(task.Turns[len(task.Turns)-1])
}

func taskTurns(task eval.SWETask) []string {
	if len(task.Turns) > 0 {
		turns := make([]string, 0, len(task.Turns))
		for _, turn := range task.Turns {
			if turn = strings.TrimSpace(turn); turn != "" {
				turns = append(turns, turn)
			}
		}
		if len(turns) > 0 {
			return turns
		}
	}
	if prompt := taskPrompt(task); prompt != "" {
		return []string{prompt}
	}
	return nil
}

func taskEnvEntries(env map[string]string) []string {
	entries := make([]string, 0, len(env))
	for key, value := range env {
		key = strings.TrimSpace(key)
		if key == "" || eval.ReservedSWETaskEnvKey(key) {
			continue
		}
		entries = append(entries, key+"="+value)
	}
	return entries
}

func taskFixturePath(suitePath string, task eval.SWETask) string {
	if strings.TrimSpace(task.Fixture) == "" {
		return ""
	}
	if filepath.IsAbs(task.Fixture) {
		return filepath.Clean(task.Fixture)
	}
	return filepath.Clean(filepath.Join(filepath.Dir(suitePath), task.Fixture))
}

func fileMissingOrEmpty(path string) bool {
	info, err := os.Stat(path)
	return err != nil || info.Size() == 0
}
