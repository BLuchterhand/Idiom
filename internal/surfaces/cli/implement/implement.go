package implement

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"idiom/internal/contracts/autonomy"
	lanecontract "idiom/internal/contracts/lane"
	queuecontract "idiom/internal/contracts/queue"
	"idiom/internal/contracts/writerroute"
	"idiom/internal/evidence/telemetry"
	"idiom/internal/platform/config"
	"idiom/internal/platform/jsonevents"
	"idiom/internal/platform/sessionid"
	"idiom/internal/runtime/queueadmission"
	"idiom/internal/runtime/writerrouting"
	cmdruntime "idiom/internal/surfaces/cli/runtime"
	cmdtelemetry "idiom/internal/surfaces/cli/telemetry"
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
	cfg.Workspace.SessionID = sessionid.New()
	telemetry.LogPhase(rec, "implement_start", map[string]any{
		"json_events": opts.JSONEvents,
		"resume":      strings.TrimSpace(opts.Resume) != "",
		"session_id":  cfg.Workspace.SessionID,
		"local_ops":   cmdtelemetry.LocalOpsFields(cfg, "implement"),
	})

	decision := writerrouting.BuildDecision(cfg, writerrouting.DecisionOptions{
		Mode:              writerroute.ModeAgenticGuarded,
		Lane:              string(lanecontract.LaneImplement),
		TaskFamily:        string(autonomy.TaskFamilyImplementation),
		ReasonCodes:       []writerroute.ReasonCode{writerroute.ReasonLocalModelDefault},
		VerificationOwner: implementWriterRouteVerificationOwner(opts),
	})
	if err := emitImplementJSONEvent(stdout, opts.JSONEvents, writerroute.EventWriterRouteDecision, decision.EventFields()); err != nil {
		fmt.Fprintf(stderr, "implement: write json event: %v\n", err)
		return 1
	}

	queueResult := implementAcquireQueueSlot(cfg, cfg.Workspace.SessionID, stdout, stderr, opts.JSONEvents, rec)
	defer queueResult.Release()
	if !queueResult.Allowed {
		fmt.Fprintf(stderr, "implement: queue admission required but unavailable: state=%s pool=%s reason=%s\n", queueResult.State, queueResult.Pool, queueResult.Reason)
		return 1
	}

	prompt, err := implementPrompt(opts)
	if err != nil {
		fmt.Fprintln(stderr, err)

		return 1
	}

	answer, err := promptRun(cfg, prompt, runOptions{
		JSONEvents:               opts.JSONEvents,
		Resume:                   opts.Resume,
		TaskFamily:               string(autonomy.TaskFamilyImplementation),
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

func implementWriterRouteVerificationOwner(opts implementOptions) writerroute.VerificationOwner {
	if opts.PlannerOwnedVerification && !opts.AllowBuildTests {
		return writerroute.VerificationOwnerPlannerOwned
	}

	return writerroute.VerificationOwnerExecutor
}

// implementAcquireQueueSlot wraps the shared queue admission contract with
// implement-specific labels and event names.
func implementAcquireQueueSlot(cfg config.Config, sessionID string, stdout io.Writer, stderr io.Writer, jsonEvents bool, rec *telemetry.Recorder) queueadmission.Result {
	label := "implement:" + sessionID
	client := fmt.Sprintf("idiom-implement/%d", os.Getpid())

	return queueadmission.AcquireWithResult(cfg, queueadmission.Options{
		Command:        "implement",
		EventPrefix:    queuecontract.CommandEventPrefixImplement,
		TelemetryEvent: "implement_queue",
		Label:          label,
		Client:         client,
		JSONEvents:     jsonEvents,
		Mode:           queuecontract.AdmissionModeRequired,
	}, rec, stdout, stderr)
}

func emitImplementJSONEvent(stdout io.Writer, jsonEvents bool, eventType writerroute.EventType, fields map[string]any) error {
	eventName, err := writerroute.CommandEventName(writerroute.CommandSurfaceImplement, eventType)
	if err != nil {
		return err
	}

	return jsonevents.Emit(stdout, jsonEvents, eventName, fields)
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
