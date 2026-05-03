package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"idiom/internal/config"
)

type artifactOptions struct {
	ConfigPath string
	Prompt     string
	PromptFile string
	OutputPath string
	JSONOut    string
	Validator  string
	Model      string
	Resume     string

	NoTools     bool
	FailOnTools bool
}

func runArtifactMain(args []string, stdout io.Writer, stderr io.Writer, promptRun promptRunner) int {
	opts, err := parseArtifactOptions(args, stderr)
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

	prompt, err := artifactPrompt(opts)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if opts.NoTools {
		prompt = noToolsArtifactPrompt(prompt)
		opts.FailOnTools = true
	}

	var events bytes.Buffer
	answer, err := promptRun(cfg, prompt, runOptions{JSONEvents: true, Resume: opts.Resume}, &events)
	if writeErr := writeOptionalFile(opts.JSONOut, events.Bytes()); writeErr != nil {
		fmt.Fprintln(stderr, writeErr)
		return 1
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if opts.FailOnTools {
		used, tool, err := artifactEventsUsedTools(events.Bytes())
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		if used {
			fmt.Fprintf(stderr, "artifact run used tools despite no-tool contract: %s\n", tool)
			return 1
		}
	}

	artifactPath, cleanup, err := materializeArtifactForValidation(answer, opts.OutputPath, opts.Validator)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if opts.Validator != "" {
		if err := runArtifactValidator(opts.Validator, artifactPath, stderr); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	}
	if opts.OutputPath == "" || opts.OutputPath == "-" {
		fmt.Fprintln(stdout, answer)
	}
	return 0
}

func parseArtifactOptions(args []string, stderr io.Writer) (artifactOptions, error) {
	opts := artifactOptions{ConfigPath: "config.json"}
	fs := flag.NewFlagSet("idiom artifact", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opts.ConfigPath, "config", opts.ConfigPath, "path to config file")
	fs.StringVar(&opts.Prompt, "prompt", "", "artifact prompt text")
	fs.StringVar(&opts.PromptFile, "prompt-file", "", "read artifact prompt from file")
	fs.StringVar(&opts.OutputPath, "output", "", "write final artifact to path; default stdout")
	fs.StringVar(&opts.JSONOut, "json-out", "", "write raw JSONL event transcript to path")
	fs.StringVar(&opts.Validator, "validator", "", "shell command to validate artifact; path is in $IDIOM_ARTIFACT")
	fs.StringVar(&opts.Model, "model", "", "override llama_server.model from config")
	fs.StringVar(&opts.Resume, "resume", "", "resume a prior session before running")
	fs.BoolVar(&opts.NoTools, "no-tools", false, "ask the model not to use tools and fail if any tool events occur")
	fs.BoolVar(&opts.FailOnTools, "fail-on-tools", false, "fail if any tool request/result events occur")
	if err := fs.Parse(args); err != nil {
		return opts, err
	}
	if fs.NArg() != 0 {
		return opts, fmt.Errorf("unexpected positional argument: %s", fs.Arg(0))
	}
	if strings.TrimSpace(opts.Prompt) != "" && strings.TrimSpace(opts.PromptFile) != "" {
		return opts, fmt.Errorf("--prompt and --prompt-file are mutually exclusive")
	}
	if strings.TrimSpace(opts.Prompt) == "" && strings.TrimSpace(opts.PromptFile) == "" {
		return opts, fmt.Errorf("--prompt or --prompt-file is required")
	}
	return opts, nil
}

func artifactPrompt(opts artifactOptions) (string, error) {
	if opts.PromptFile == "" {
		return strings.TrimSpace(opts.Prompt), nil
	}
	data, err := os.ReadFile(filepath.Clean(opts.PromptFile))
	if err != nil {
		return "", fmt.Errorf("read prompt file: %w", err)
	}
	prompt := strings.TrimSpace(string(data))
	if prompt == "" {
		return "", fmt.Errorf("prompt file is empty")
	}
	return prompt, nil
}

func noToolsArtifactPrompt(prompt string) string {
	return strings.TrimSpace(`# Artifact Generation Task

Produce the requested artifact as the final answer.

IMPORTANT:
- Do not call tools.
- Do not inspect the workspace.
- Do not edit files.
- Output the artifact only, with no preface or analysis unless the prompt explicitly asks for it.

## Artifact Request

` + prompt)
}

func artifactEventsUsedTools(data []byte) (bool, string, error) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			return false, "", fmt.Errorf("parse artifact JSONL event: %w", err)
		}
		eventType, _ := event["type"].(string)
		if eventType != "tool_request" && eventType != "tool_result" {
			continue
		}
		tool, _ := event["tool"].(string)
		if tool == "" {
			tool = eventType
		}
		return true, tool, nil
	}
	if err := scanner.Err(); err != nil {
		return false, "", fmt.Errorf("scan artifact JSONL events: %w", err)
	}
	return false, "", nil
}

func writeOptionalFile(path string, data []byte) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	clean := filepath.Clean(path)
	if err := os.MkdirAll(filepath.Dir(clean), 0o755); err != nil {
		return fmt.Errorf("create parent directory for %s: %w", clean, err)
	}
	if err := os.WriteFile(clean, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", clean, err)
	}
	return nil
}

func materializeArtifactForValidation(answer string, outputPath string, validator string) (string, func(), error) {
	if outputPath != "" && outputPath != "-" {
		if err := writeOptionalFile(outputPath, []byte(answer)); err != nil {
			return "", nil, err
		}
		return filepath.Clean(outputPath), nil, nil
	}
	if validator == "" {
		return "", nil, nil
	}
	file, err := os.CreateTemp("", "idiom-artifact-*")
	if err != nil {
		return "", nil, fmt.Errorf("create temporary artifact: %w", err)
	}
	path := file.Name()
	cleanup := func() { _ = os.Remove(path) }
	if _, err := io.WriteString(file, answer); err != nil {
		_ = file.Close()
		cleanup()
		return "", nil, fmt.Errorf("write temporary artifact: %w", err)
	}
	if err := file.Close(); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("close temporary artifact: %w", err)
	}
	return path, cleanup, nil
}

func runArtifactValidator(command string, artifactPath string, stderr io.Writer) error {
	if artifactPath == "" {
		return fmt.Errorf("validator requires a materialized artifact path")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command)
	cmd.Env = append(os.Environ(), "IDIOM_ARTIFACT="+artifactPath)
	cmd.Stdout = stderr
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("artifact validator timed out: %w", ctx.Err())
		}
		return fmt.Errorf("artifact validator failed: %w", err)
	}
	return nil
}
