package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"idiom/internal/config"
	"idiom/internal/telemetry"
)

type implementOptions struct {
	ConfigPath               string
	Prompt                   string
	PromptFile               string
	Model                    string
	Resume                   string
	JSONEvents               bool
	PlannerOwnedVerification bool
	AllowBuildTests          bool
}

func runImplementMain(args []string, stdout io.Writer, stderr io.Writer, promptRun promptRunner) int {
	return runImplementMainWithTelemetry(args, stdout, stderr, promptRun, nil)
}

func runImplementMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, promptRun promptRunner, rec *telemetry.Recorder) int {
	opts, err := parseImplementOptions(args, stderr)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}

	cfg, err := config.Load(opts.ConfigPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if strings.TrimSpace(opts.Model) != "" {
		cfg.LlamaServer.Model = opts.Model
	}
	logCLIPhase(rec, "implement_start", map[string]any{
		"json_events": opts.JSONEvents,
		"resume":      strings.TrimSpace(opts.Resume) != "",
	})

	prompt, err := implementPrompt(opts)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	answer, err := promptRun(cfg, prompt, runOptions{
		JSONEvents:               opts.JSONEvents,
		Resume:                   opts.Resume,
		TaskFamily:               "implementation",
		PlannerOwnedVerification: opts.PlannerOwnedVerification && !opts.AllowBuildTests,
		Telemetry:                rec,
	}, stdout)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if !opts.JSONEvents {
		fmt.Fprintln(stdout, answer)
	}
	return 0
}

func parseImplementOptions(args []string, stderr io.Writer) (implementOptions, error) {
	var opts implementOptions
	fs := flag.NewFlagSet("idiom implement", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opts.ConfigPath, "config", "config.json", "path to config file")
	fs.StringVar(&opts.Prompt, "prompt", "", "implementation prompt")
	fs.StringVar(&opts.PromptFile, "prompt-file", "", "read implementation prompt from a file")
	fs.BoolVar(&opts.JSONEvents, "json", false, "emit structured JSONL events to stdout")
	fs.StringVar(&opts.Resume, "resume", "", "resume a prior session by id before running")
	fs.StringVar(&opts.Model, "model", "", "override llama_server.model from config")
	fs.BoolVar(&opts.PlannerOwnedVerification, "planner-owned-verification", false, "delegate build/test verification to the caller; implementation may only run cheap non-build sanity checks")
	fs.BoolVar(&opts.AllowBuildTests, "allow-build-tests", false, "compatibility flag: keep build/test shell commands available inside the implementation lane")
	if err := fs.Parse(args); err != nil {
		return implementOptions{}, err
	}
	if fs.NArg() > 0 {
		if strings.TrimSpace(opts.Prompt) != "" || strings.TrimSpace(opts.PromptFile) != "" {
			return implementOptions{}, fmt.Errorf("positional prompt cannot be combined with --prompt or --prompt-file")
		}
		opts.Prompt = strings.Join(fs.Args(), " ")
	}
	if strings.TrimSpace(opts.Prompt) == "" && strings.TrimSpace(opts.PromptFile) == "" {
		return implementOptions{}, fmt.Errorf("provide --prompt, --prompt-file, or a positional prompt")
	}
	return opts, nil
}

func implementPrompt(opts implementOptions) (string, error) {
	hasPrompt := strings.TrimSpace(opts.Prompt) != ""
	hasFile := strings.TrimSpace(opts.PromptFile) != ""
	if hasPrompt && hasFile {
		return "", fmt.Errorf("--prompt and --prompt-file are mutually exclusive")
	}
	if !hasPrompt && !hasFile {
		return "", fmt.Errorf("provide --prompt, --prompt-file, or a positional prompt")
	}
	if hasPrompt {
		return opts.Prompt, nil
	}
	data, err := os.ReadFile(opts.PromptFile)
	if err != nil {
		return "", fmt.Errorf("read prompt file: %w", err)
	}
	prompt := strings.TrimSpace(string(data))
	if prompt == "" {
		return "", fmt.Errorf("prompt file %q is empty", opts.PromptFile)
	}
	return prompt, nil
}
