package planexecute

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

func ParseOptions(args []string, stderr io.Writer) (Options, error) {
	var opts Options

	fs := flag.NewFlagSet("idiom plan-execute", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opts.ConfigPath, "config", "config.json", "path to config file")
	fs.StringVar(&opts.Prompt, "prompt", "", "implementation prompt")
	fs.StringVar(&opts.PromptFile, "prompt-file", "", "read implementation prompt from a file")
	fs.BoolVar(&opts.JSONEvents, "json", false, "emit structured JSONL events to stdout")
	fs.StringVar(&opts.Resume, "resume", "", "resume a prior session by id before running")
	fs.StringVar(&opts.SessionID, "session", "", "plan-execute session id for a new run")
	fs.StringVar(&opts.Model, "model", "", "override llama_server.model from config")
	fs.BoolVar(&opts.DisableThinking, "no-thinking", false, "force request-level Qwen thinking off for this planner run")
	fs.IntVar(&opts.MaxTokens, "max-tokens", 0, "override planner max_tokens for this run; 0 uses the shape-aware plan-execute default")
	fs.BoolVar(&opts.SkipReviewRepair, "skip-review-repair", false, "skip the post-execute review-repair planner pass")
	fs.Var(&opts.RequiredEditPaths, "required-edit-path", "workspace-relative required edit path for this plan-execute run; repeatable and augments prompt extraction")
	fs.Var(&opts.AllowedEditPaths, "allowed-edit-path", "workspace-relative candidate edit path for this plan-execute run; repeatable and augments prompt extraction without requiring coverage")
	fs.Var(&opts.WriteAllowedPaths, "write-allowed-path", "workspace-relative write envelope path for this plan-execute run; repeatable and limits writes without adding coverage")
	fs.BoolVar(&opts.WriteAllowedPathsExplicit, "write-allowed-paths-explicit", false, "treat write-allowed-path as an explicit write envelope even when empty")
	fs.Var((*MultiFlag)(&opts.AuthorizedNewFiles), "authorized-new-file", "workspace-relative new file path authorized by an upstream typed plan contract; repeatable")

	if err := fs.Parse(args); err != nil {
		return Options{}, err
	}

	if fs.NArg() > 0 {
		if strings.TrimSpace(opts.Prompt) != "" || strings.TrimSpace(opts.PromptFile) != "" {
			return Options{}, fmt.Errorf("positional prompt cannot be combined with --prompt or --prompt-file")
		}

		opts.Prompt = strings.Join(fs.Args(), " ")
	}

	if strings.TrimSpace(opts.Prompt) == "" && strings.TrimSpace(opts.PromptFile) == "" {
		return Options{}, fmt.Errorf("provide --prompt, --prompt-file, or a positional prompt")
	}

	return opts, nil
}

func planExecutePrompt(opts Options) (string, error) {
	hasPrompt := strings.TrimSpace(opts.Prompt) != ""
	hasFile := strings.TrimSpace(opts.PromptFile) != ""

	if hasPrompt && hasFile {
		return "", fmt.Errorf("--prompt and --prompt-file are mutually exclusive")
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
