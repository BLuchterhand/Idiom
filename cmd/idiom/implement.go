package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"idiom/internal/config"
	"idiom/internal/runner"
	"idiom/internal/telemetry"
	"idiom/internal/writerroute"
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
	// MaxTokens overrides the per-call max_tokens cap. Zero falls
	// through to the backend default (8192 for the shipped MLX
	// config). Refine's per-iter dispatch passes 16384 here to give
	// thinking-mode 35B headroom to finish reasoning AND produce the
	// deliverable inside the same generation budget (#96).
	MaxTokens       int
	DisableThinking bool
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
	cfg.Workspace.SessionID = newPlanExecuteSessionID()
	logCLIPhase(rec, "implement_start", map[string]any{
		"json_events": opts.JSONEvents,
		"resume":      strings.TrimSpace(opts.Resume) != "",
		"session_id":  cfg.Workspace.SessionID,
		"local_ops":   localOpsTelemetryFields(cfg, "implement"),
	})
	decision := buildCLIWriterRouteDecision(cfg, cliWriterRouteOptions{
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

// implementAcquireQueueSlot mirrors planExecuteAcquireQueueSlot —
// see that helper for the full contract. Resolves a pool from the
// configured model alias (or IDIOM_QUEUE_POOL override) and acquires
// from the daemon at IDIOM_QUEUE_SOCKET / DefaultSocketPath() when
// reachable. Returns a no-op release on daemon absence.
func implementAcquireQueueSlot(cfg config.Config, sessionID string, stdout io.Writer, stderr io.Writer, jsonEvents bool, rec *telemetry.Recorder) func() {
	pool := strings.TrimSpace(os.Getenv("IDIOM_QUEUE_POOL"))
	if pool == "" {
		pool = runner.PoolForModelAlias(cfg.LlamaServer.Model)
	}
	if pool == "" {
		logImplementQueueTelemetry(rec, stdout, jsonEvents, "disabled", "", 0, "pool_disabled", false)
		return func() {}
	}
	socket := strings.TrimSpace(os.Getenv("IDIOM_QUEUE_SOCKET"))
	label := "implement:" + sessionID
	client := fmt.Sprintf("idiom-implement/%d", os.Getpid())
	startedAt := time.Now()
	result := runner.MaybeAcquireWithResult(context.Background(), socket, pool, label, client, stderr)
	waitMillis := time.Since(startedAt).Milliseconds()
	if result.Err != nil {
		fmt.Fprintf(stderr, "[idiom queue %s] acquire failed: %v — proceeding un-gated\n", pool, result.Err)
	}
	state := string(result.State)
	if state == "" {
		state = "error"
	}
	reason := result.Reason
	degraded := state == "unavailable" || state == "degraded" || state == "error"
	logImplementQueueTelemetry(rec, stdout, jsonEvents, state, pool, waitMillis, reason, degraded)
	if result.Release == nil {
		return func() {}
	}
	return result.Release
}

func logImplementQueueTelemetry(rec *telemetry.Recorder, stdout io.Writer, jsonEvents bool, state string, pool string, waitMillis int64, reason string, degraded bool) {
	fields := map[string]any{
		"state":        state,
		"pool":         pool,
		"wait_ms":      waitMillis,
		"degraded":     degraded,
		"reason_class": queueReasonClass(reason),
	}
	emitImplementJSONEvent(stdout, jsonEvents, implementQueueEventName(state), fields)
	logCLIPhase(rec, "implement_queue", fields)
}

func implementQueueEventName(state string) string {
	switch state {
	case "acquired":
		return "queue_acquired"
	case "unavailable":
		return "queue_unavailable"
	case "disabled":
		return "queue_disabled"
	case "degraded":
		return "queue_degraded"
	default:
		return "queue_error"
	}
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

func queueReasonClass(reason string) string {
	reason = strings.ToLower(strings.TrimSpace(reason))
	switch {
	case reason == "":
		return ""
	case strings.Contains(reason, "pool_disabled"):
		return "pool_disabled"
	case strings.Contains(reason, "nested_same_pool"):
		return "nested_same_pool"
	case strings.Contains(reason, "nested_cross_pool"):
		return "nested_cross_pool"
	case strings.Contains(reason, "unknown pool"):
		return "unknown_pool"
	case strings.Contains(reason, "daemon_unreachable"), strings.Contains(reason, "unreachable"), strings.Contains(reason, "connection refused"):
		return "daemon_unreachable"
	case strings.Contains(reason, "context canceled"), strings.Contains(reason, "deadline"):
		return "timeout_or_canceled"
	default:
		return "queue_error"
	}
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
