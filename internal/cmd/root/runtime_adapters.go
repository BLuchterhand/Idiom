package root

import (
	"io"

	"idiom/internal/cmdruntime"
	"idiom/internal/config"
	"idiom/internal/telemetry"
)

type runOptions = cmdruntime.RunOptions
type promptRunner = cmdruntime.PromptRunner
type promptsRunner = cmdruntime.PromptsRunner
type modeRunner = cmdruntime.ModeRunner
type listSessionsRunner = cmdruntime.ListSessionsRunner

func run(args []string, stdout io.Writer, stderr io.Writer, promptRun promptRunner, promptsRun promptsRunner, tuiRun modeRunner, serverRun modeRunner, listSessions listSessionsRunner) int {
	return cmdruntime.Run(args, stdout, stderr, promptRun, promptsRun, tuiRun, serverRun, listSessions, runVersionMain)
}

func runWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, promptRun promptRunner, promptsRun promptsRunner, tuiRun modeRunner, serverRun modeRunner, listSessions listSessionsRunner, rec *telemetry.Recorder) int {
	return cmdruntime.RunWithTelemetry(args, stdout, stderr, promptRun, promptsRun, tuiRun, serverRun, listSessions, runVersionMain, rec)
}

func runTUI(cfg config.Config, opts runOptions) error {
	return cmdruntime.RunTUI(cfg, opts)
}

func runPromptOnce(cfg config.Config, prompt string, opts runOptions, stdout io.Writer) (string, error) {
	return cmdruntime.RunPromptOnce(cfg, prompt, opts, stdout)
}

func runPromptsOnce(cfg config.Config, prompts []string, opts runOptions, stdout io.Writer) ([]string, error) {
	return cmdruntime.RunPromptsOnce(cfg, prompts, opts, stdout)
}

func sessionIDFromPath(path string) string {
	return cmdruntime.SessionIDFromPath(path)
}

func runListSessions(cfg config.Config, stdout io.Writer) error {
	return cmdruntime.RunListSessions(cfg, stdout)
}

func runServerOnly(cfg config.Config, opts runOptions) error {
	return cmdruntime.RunServerOnly(cfg, opts)
}
