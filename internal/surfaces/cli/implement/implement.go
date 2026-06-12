package implement

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"idiom/internal/contracts/writerroute"
	"idiom/internal/evidence/telemetry"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/queueadmission"
	planexecutecmd "idiom/internal/surfaces/cli/planexecute"
	"idiom/internal/surfaces/cli/route"
	"idiom/internal/surfaces/cli/runtime"
	"idiom/internal/surfaces/cli/telemetry"
	toolcmd "idiom/internal/surfaces/cli/tool"
)

type runOptions = cmdruntime.RunOptions
type promptRunner = cmdruntime.PromptRunner

type implementOptions struct {
	ConfigPath               string
	Prompt                   string
	PromptFile               string
	Model                    string
	Resume                   string
	JSONEvents               bool
	PlannerOwnedVerification bool
	AllowBuildTests          bool
	// MaxTokens overrides the per-call max_tokens cap. Zero falls
	// through to the backend default (8192 for the shipped MLX
	// config). Refine's per-iter dispatch passes 16384 here to give
	// thinking-mode 35B headroom to finish reasoning AND produce the
	// deliverable inside the same generation budget (#96).
	MaxTokens       int
	DisableThinking bool
}

func RunMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, promptRun cmdruntime.PromptRunner, rec *telemetry.Recorder) int {
	return runImplementMainWithTelemetry(args, stdout, stderr, promptRun, rec)
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

	// Stamp a session UUID so concurrent `idiom implement` runs in the
	// same workspace produce attributable conversation logs and (if
	// they ever submit a plan) attributable plan artifacts. Mirrors
	// the equivalent generation in runPlanExecuteMain.
	cfg.Workspace.SessionID = planexecutecmd.NewSessionID()
	toolcmd.LogCLIPhase(rec, "implement_start", map[string]any{
		"json_events": opts.JSONEvents,
		"resume":      strings.TrimSpace(opts.Resume) != "",
		"session_id":  cfg.Workspace.SessionID,
		"local_ops":   cmdtelemetry.LocalOpsFields(cfg, "implement"),
	})

	decision := cmdroute.BuildDecision(cfg, cmdroute.Options{
		Mode:              writerroute.ModeAgenticGuarded,
		Lane:              "implement",
		TaskFamily:        "implementation",
		ReasonCodes:       []string{"route.local_model_default"},
		VerificationOwner: implementWriterRouteVerificationOwner(opts),
	})
	emitImplementJSONEvent(stdout, opts.JSONEvents, "writer_route_decision", decision.EventFields())

	// Queue gate: same opportunistic acquire pattern as plan-execute.
	// When the queue daemon is up, concurrent `idiom implement` runs
	// serialize through the configured pool instead of trampling the
	// MLX backend at once.
	queueRelease := implementAcquireQueueSlot(cfg, cfg.Workspace.SessionID, stdout, stderr, opts.JSONEvents, rec)
	defer queueRelease()

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
		MaxTokensOverride:        opts.MaxTokens,
		DisableThinking:          opts.DisableThinking,
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

func implementWriterRouteVerificationOwner(opts implementOptions) string {
	if opts.PlannerOwnedVerification && !opts.AllowBuildTests {
		return "planner_owned"
	}

	return "executor"
}

// implementAcquireQueueSlot wraps the shared queue admission contract with
// implement-specific labels and event names.
func implementAcquireQueueSlot(cfg config.Config, sessionID string, stdout io.Writer, stderr io.Writer, jsonEvents bool, rec *telemetry.Recorder) func() {
	label := "implement:" + sessionID
	client := fmt.Sprintf("idiom-implement/%d", os.Getpid())

	return queueadmission.Acquire(cfg, queueadmission.Options{
		Command:        "implement",
		EventPrefix:    "idiom.implement",
		TelemetryEvent: "implement_queue",
		Label:          label,
		Client:         client,
		JSONEvents:     jsonEvents,
	}, rec, stdout, stderr)
}

func emitImplementJSONEvent(stdout io.Writer, jsonEvents bool, eventType string, fields map[string]any) {
	if !jsonEvents {
		return
	}

	payload := map[string]any{
		"type": "idiom.implement." + eventType,
		"ts":   time.Now().UTC().Format(time.RFC3339Nano),
	}
	for key, value := range fields {
		payload[key] = value
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return
	}

	fmt.Fprintln(stdout, string(body))
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
	fs.BoolVar(&opts.DisableThinking, "no-thinking", false, "force request-level Qwen thinking off for this run")
	fs.IntVar(&opts.MaxTokens, "max-tokens", 0, "override chat max_tokens for this implementation run; 0 uses backend default (8192)")

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
