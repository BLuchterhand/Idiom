package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"idiom/internal/config"
)

type implementOptions struct {
	ConfigPath string
	Prompt     string
	PromptFile string
	Model      string
	Resume     string
	JSONEvents bool
}

func runImplementMain(args []string, stdout io.Writer, stderr io.Writer, promptRun promptRunner) int {
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

	prompt, err := implementPrompt(opts)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	answer, err := promptRun(cfg, prompt, runOptions{
		JSONEvents: opts.JSONEvents,
		Resume:     opts.Resume,
		TaskFamily: "implementation",
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
