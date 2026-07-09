package artifact

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"idiom/internal/evidence/telemetry"
	"idiom/internal/platform/commandexec"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/workerruntime"
	"idiom/internal/workflows/productworkflow"
)

type RunOptions struct {
	JSONEvents        bool
	Resume            string
	TaskFamily        string
	MaxTokensOverride int
	Telemetry         *telemetry.Recorder
}

type PromptRunner func(config.Config, string, RunOptions, io.Writer) (string, error)

type artifactOptions struct {
	ConfigPath string
	Prompt     string
	PromptFile string
	OutputPath string
	JSONOut    string
	ReportOut  string
	Validator  string
	Model      string
	Resume     string
	MaxTokens  int

	NoTools          bool
	FailOnTools      bool
	MechanicalRepair bool
}

type artifactReport struct {
	SchemaVersion            string `json:"schema_version"`
	CreatedAt                string `json:"created_at"`
	ArtifactContract         string `json:"artifact_contract"`
	Model                    string `json:"model,omitempty"`
	OutputPath               string `json:"output_path,omitempty"`
	JSONOut                  string `json:"json_out,omitempty"`
	ReportOut                string `json:"report_out,omitempty"`
	Validator                string `json:"validator,omitempty"`
	ValidatorExit            int    `json:"validator_exit"`
	ValidatorError           string `json:"validator_error,omitempty"`
	NoTools                  bool   `json:"no_tools"`
	FailOnTools              bool   `json:"fail_on_tools"`
	ToolUseDetected          bool   `json:"tool_use_detected"`
	ToolName                 string `json:"tool_name,omitempty"`
	PromptHash               string `json:"prompt_hash,omitempty"`
	AnswerHash               string `json:"answer_hash,omitempty"`
	AnswerBytes              int    `json:"answer_bytes,omitempty"`
	RepairSource             string `json:"repair_source"`
	MechanicalRepairApplied  bool   `json:"mechanical_repair_applied"`
	ManualModifiedAfterModel bool   `json:"manual_modified_after_model"`
	DispatchReady            bool   `json:"dispatch_ready"`
	Error                    string `json:"error,omitempty"`
}

func RunMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, promptRun PromptRunner, rec *telemetry.Recorder) int {
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

	telemetry.LogPhase(rec, "artifact_start", map[string]any{
		"has_output":          strings.TrimSpace(opts.OutputPath) != "",
		"has_json_out":        strings.TrimSpace(opts.JSONOut) != "",
		"has_report_out":      strings.TrimSpace(opts.ReportOut) != "",
		"has_validator":       strings.TrimSpace(opts.Validator) != "",
		"no_tools":            opts.NoTools,
		"mechanical_repair":   opts.MechanicalRepair,
		"max_tokens_override": opts.MaxTokens,
	})

	rawPrompt, err := artifactPrompt(opts)
	if err != nil {
		fmt.Fprintln(stderr, err)

		return 1
	}

	report := newArtifactReport(opts, cfg, rawPrompt)
	prompt := rawPrompt

	if opts.NoTools {
		prompt = noToolsArtifactPrompt(prompt)
		opts.FailOnTools = true
		report.FailOnTools = true
	}

	runOpts := RunOptions{JSONEvents: true, Resume: opts.Resume, MaxTokensOverride: opts.MaxTokens, Telemetry: rec}
	if opts.NoTools {
		runOpts.TaskFamily = workerruntime.ArtifactFinalAnswerTaskFamily
	}

	var events bytes.Buffer

	answer, err := promptRun(cfg, prompt, runOpts, &events)

	if writeErr := writeOptionalFile(opts.JSONOut, events.Bytes()); writeErr != nil {
		fmt.Fprintln(stderr, writeErr)

		return 1
	}

	if err != nil {
		report.Error = err.Error()
		_ = writeArtifactReport(opts.ReportOut, report)

		fmt.Fprintln(stderr, err)

		return 1
	}

	if opts.MechanicalRepair {
		if repaired, changed := repairArtifactMechanically(answer); changed {
			answer = repaired
			report.RepairSource = "mechanical"
			report.MechanicalRepairApplied = true
		}
	}

	report.AnswerHash = sha256HexString(answer)
	report.AnswerBytes = len(answer)

	if opts.FailOnTools {
		used, tool, err := artifactEventsUsedTools(events.Bytes())
		if err != nil {
			report.Error = err.Error()
			_ = writeArtifactReport(opts.ReportOut, report)

			fmt.Fprintln(stderr, err)

			return 1
		}

		if used {
			report.ToolUseDetected = true
			report.ToolName = tool
			report.Error = "artifact run used tools despite no-tool contract: " + tool
			_ = writeArtifactReport(opts.ReportOut, report)
			fmt.Fprintln(stderr, report.Error)

			return 1
		}
	}

	artifactPath, cleanup, err := materializeArtifactForValidation(answer, opts.OutputPath, opts.Validator)
	if cleanup != nil {
		defer cleanup()
	}

	if err != nil {
		report.Error = err.Error()
		_ = writeArtifactReport(opts.ReportOut, report)

		fmt.Fprintln(stderr, err)

		return 1
	}

	if opts.Validator != "" {
		exit, err := runArtifactValidator(opts.Validator, artifactPath, stderr)

		report.ValidatorExit = exit

		if err != nil {
			report.ValidatorError = err.Error()
			report.Error = err.Error()
			_ = writeArtifactReport(opts.ReportOut, report)

			fmt.Fprintln(stderr, err)

			return 1
		}
	}

	if opts.OutputPath == "" || opts.OutputPath == "-" {
		fmt.Fprintln(stdout, answer)
	}

	report.DispatchReady = true
	if err := writeArtifactReport(opts.ReportOut, report); err != nil {
		fmt.Fprintln(stderr, err)

		return 1
	}

	telemetry.LogPhase(rec, "artifact_written", map[string]any{
		"output_path":         strings.TrimSpace(opts.OutputPath),
		"report_out":          strings.TrimSpace(opts.ReportOut),
		"answer_len":          len(answer),
		"answer_hash":         report.AnswerHash,
		"repair_source":       report.RepairSource,
		"dispatch_ready":      report.DispatchReady,
		"validator_exit":      report.ValidatorExit,
		"tool_use_detected":   report.ToolUseDetected,
		"artifact_contract":   report.ArtifactContract,
		"mechanical_repaired": report.MechanicalRepairApplied,
	})

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
	fs.StringVar(&opts.ReportOut, "report-out", "", "write artifact provenance/readiness report JSON to path")
	fs.StringVar(&opts.Validator, "validator", "", "shell command to validate artifact; path is in $IDIOM_ARTIFACT")
	fs.StringVar(&opts.Model, "model", "", "override llama_server.model from config")
	fs.StringVar(&opts.Resume, "resume", "", "resume a prior session before running")
	fs.IntVar(&opts.MaxTokens, "max-token", 0, "override chat max_tokens for this artifact run; 0 uses config/routing defaults")
	fs.IntVar(&opts.MaxTokens, "max-tokens", 0, "alias for --max-token")
	fs.BoolVar(&opts.NoTools, "no-tools", false, "ask the model not to use tools and fail if any tool events occur")
	fs.BoolVar(&opts.FailOnTools, "fail-on-tools", false, "fail if any tool request/result events occur")
	fs.BoolVar(&opts.MechanicalRepair, "mechanical-repair", false, "apply deterministic artifact-only cleanup before writing/validation")

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

	if opts.MaxTokens < 0 {
		return opts, fmt.Errorf("--max-token must be non-negative")
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

func newArtifactReport(opts artifactOptions, cfg config.Config, prompt string) artifactReport {
	return artifactReport{
		SchemaVersion:            "idiom.artifact.report.v1",
		CreatedAt:                time.Now().UTC().Format(time.RFC3339Nano),
		ArtifactContract:         artifactContract(opts),
		Model:                    strings.TrimSpace(cfg.LlamaServer.Model),
		OutputPath:               strings.TrimSpace(opts.OutputPath),
		JSONOut:                  strings.TrimSpace(opts.JSONOut),
		ReportOut:                strings.TrimSpace(opts.ReportOut),
		Validator:                strings.TrimSpace(opts.Validator),
		ValidatorExit:            0,
		NoTools:                  opts.NoTools,
		FailOnTools:              opts.FailOnTools,
		PromptHash:               sha256HexString(prompt),
		RepairSource:             "raw_model",
		ManualModifiedAfterModel: false,
	}
}

func artifactContract(opts artifactOptions) string {
	if opts.NoTools {
		return "artifact.no_tools.v1"
	}

	return "artifact.v1"
}

func writeArtifactReport(path string, report artifactReport) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}

	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal artifact report: %w", err)
	}

	data = append(data, '\n')

	return writeOptionalFile(path, data)
}

func sha256HexString(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func repairArtifactMechanically(answer string) (string, bool) {
	unwrapped := productworkflow.UnwrapSingleMarkdownFence(answer)
	if unwrapped != answer {
		return unwrapped, true
	}

	return answer, false
}

func runArtifactValidator(command string, artifactPath string, stderr io.Writer) (int, error) {
	if artifactPath == "" {
		return 2, fmt.Errorf("validator requires a materialized artifact path")
	}

	result := commandexec.Run(context.TODO(), commandexec.Spec{
		Mode:           commandexec.ModePlainShell,
		Command:        command,
		Shell:          "/bin/sh",
		Env:            []string{"IDIOM_ARTIFACT=" + artifactPath},
		Timeout:        2 * time.Minute,
		MaxOutputBytes: commandexec.DiagnosticCaptureBytes,
		MergeOutput:    true,
	})
	if result.Stdout != "" && stderr != nil {
		_, _ = io.WriteString(stderr, result.Stdout)
	}

	if result.Err == nil {
		return 0, nil
	}

	if result.TimedOut {
		return 124, fmt.Errorf("artifact validator timed out: %w", context.DeadlineExceeded)
	}

	return result.ExitCode, fmt.Errorf("artifact validator failed: %w", result.Err)
}
